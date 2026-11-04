package syncer

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

// This is a simulated GitHub App installation: it exercises VEDA's stored
// credential and the complete sync path without using a real GitHub secret.
func TestGitHubAppStoredCredentialSync(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	secret := github.Secret{Kind: "app", AppID: 7, InstallationID: 9, PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Initial"})
	f.Modify(func() { f.Private = true })
	var mu sync.Mutex
	var permissions []string
	invalidJWT := false
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/app/installations/") {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			pieces := strings.Split(token, ".")
			valid := len(pieces) == 3
			if valid {
				signature, e := base64.RawURLEncoding.DecodeString(pieces[2])
				hash := sha256.Sum256([]byte(pieces[0] + "." + pieces[1]))
				valid = e == nil && rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature) == nil
				var claims struct {
					Iss     string `json:"iss"`
					Issued  int64  `json:"iat"`
					Expires int64  `json:"exp"`
				}
				payload, e := base64.RawURLEncoding.DecodeString(pieces[1])
				valid = valid && e == nil && json.Unmarshal(payload, &claims) == nil && claims.Iss == "7" && claims.Issued <= time.Now().Unix() && claims.Expires > time.Now().Unix()
			}
			if !valid {
				mu.Lock()
				invalidJWT = true
				mu.Unlock()
				http.Error(w, "invalid fixture signature", http.StatusUnauthorized)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/access_tokens") {
				var body struct {
					Repositories []string          `json:"repositories"`
					Permissions  map[string]string `json:"permissions"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Repositories) != 1 || body.Repositories[0] != "handbook" || len(body.Permissions) != 1 || (body.Permissions["contents"] != "read" && body.Permissions["contents"] != "write") {
					http.Error(w, "invalid fixture scope", http.StatusBadRequest)
					return
				}
				mu.Lock()
				permissions = append(permissions, body.Permissions["contents"])
				mu.Unlock()
				jsonResponse(w, 200, map[string]string{"token": githubtest.Access, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
				return
			}
			jsonResponse(w, 200, map[string]any{"app_id": 7, "account": map[string]string{"login": "org"}, "suspended_at": nil})
			return
		}
		f.Serve(w, r)
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	store := testStore(t)
	ctx := context.Background()
	if _, err = store.saveAccount(ctx, client, "app", secret); err != nil {
		t.Fatal("account verification failed", err)
	}
	store, err = store.reopen()
	if err != nil {
		t.Fatal("cannot reopen store", err)
	}
	stored, _, err := store.secret("app")
	if err != nil {
		t.Fatal("cannot load stored fixture account", err)
	}
	if _, err = client.Token(ctx, stored, "org/handbook", false); err != nil {
		t.Fatal("read-only installation token failed", err)
	}
	manager := newSyncManager(store, client)
	if _, err = manager.Connect(ctx, ConnectRequest{ID: "handbook", Repository: "org/handbook", AccountID: "app", Write: true}); err != nil {
		t.Fatal("connect failed", err)
	}
	manager.Once(ctx)
	if p, e := store.page("handbook/guide.md", false); e != nil || p.Text != "# Initial" {
		t.Fatal("private import failed", e)
	}
	if _, e := store.page("handbook/guide.md", true); e != domain.ErrNotFound {
		t.Fatal("private content exposed", e)
	}
	if _, err = store.put("handbook", "guide.md", "# Local update", 1); err != nil {
		t.Fatal(err)
	}
	manager.Once(ctx)
	if got := f.Contents()["guide.md"]; got != "# Local update" {
		t.Fatal("write-back failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if invalidJWT || len(permissions) == 0 {
		t.Fatal("app authorization was not verified")
	}
	hasRead, hasWrite := false, false
	for _, permission := range permissions {
		hasRead = hasRead || permission == "read"
		hasWrite = hasWrite || permission == "write"
	}
	if !hasRead || !hasWrite {
		t.Fatal("read and write installation scopes were not both exercised")
	}
}
