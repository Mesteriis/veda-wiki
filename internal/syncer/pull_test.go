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
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func connectFixture(t *testing.T, s *fixtureStore, f *githubtest.Fake, write bool) *Manager {
	t.Helper()
	m := newSyncManager(s, f.Client())
	account := ""
	if write || f.Private {
		if _, e := s.saveAccount(context.Background(), m.github, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); e != nil {
			t.Fatal(e)
		}
		account = "operator"
	}
	if _, e := m.Connect(context.Background(), ConnectRequest{ID: "handbook", Repository: "https://github.com/org/handbook", AccountID: account, Write: write}); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	return m
}

func TestBackgroundSyncImportsChangedGitHubHead(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Initial"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	initialVerification := s.connections()[0].VerifiedAt
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx, 20*time.Millisecond) }()
	defer func() { cancel(); <-done }()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for s.connections()[0].VerifiedAt == initialVerification {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("initial background check did not complete")
		}
	}
	remote := f.Contents()
	remote["guide.md"] = "# Updated upstream"
	f.Advance(remote)
	for {
		page, err := s.page("handbook/guide.md", false)
		if err == nil && page.Text == "# Updated upstream" {
			if _, err := s.page("handbook/guide.md", true); err == nil {
				t.Fatal("private updated article exposed publicly")
			}
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("background sync did not import updated head")
		}
	}
}

func TestStaleSyncCannotReverifyAfterPauseResume(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	m := connectFixture(t, s, f, false)
	old := s.connections()[0]
	if err := s.pauseConnection(old.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.pauseConnection(old.ID, false); err != nil {
		t.Fatal(err)
	}
	current := s.connections()[0]
	if current.Verification != "unknown" || current.ConfigRevision == old.ConfigRevision {
		t.Fatal("invalid pause fixture")
	}
	m.syncSource(context.Background(), old)
	after := s.connections()[0]
	if after.Verification != "unknown" || after.State != "pending" {
		t.Fatal("stale success changed current status")
	}
	if _, err := s.page("handbook/guide.md", true); err != domain.ErrNotFound {
		t.Fatal("stale result republished source", err)
	}
	f.Modify(func() { f.Fail = true })
	m.syncSource(context.Background(), old)
	after = s.connections()[0]
	if after.Verification != "unknown" || after.State != "pending" || after.LastError != current.LastError {
		t.Fatal("stale failure changed current status")
	}
	f.Modify(func() { f.Fail = false })
	m.Once(context.Background())
	if _, err := s.page("handbook/guide.md", true); err != nil {
		t.Fatal("fresh verification did not restore visibility", err)
	}
}

func TestGitHubPublicPullAndTrackedProgramScope(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Documentation", "README.md": "not imported", "src/main.go": "package original"})
	connectFixture(t, s, f, false)
	if p, e := s.page("handbook/docs/guide.md", true); e != nil || p.Text != "# Documentation" {
		t.Fatal(p, e)
	}
	if len(s.snapshot(true).Pages) != 1 {
		t.Fatal("program scope")
	}
	if s.db().Sources["handbook"].ConnectionID != "handbook" {
		t.Fatal("connection provenance missing")
	}
	if _, e := s.importBatch(testBatch()); !errorsIsConflictOrInvalid(e) {
		t.Fatal("manual import replaced managed source", e)
	}
}

func TestGitHubRevocationOutageAndConflictPreserveLocal(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Local", 1)
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Remote"})
	m.Once(context.Background())
	if len(s.conflicts()) != 1 || f.WriteCount() != 0 {
		t.Fatal("upstream conflict overwritten")
	}
	f.Modify(func() { f.Denied = true })
	s.retryConnection("handbook")
	m.Once(context.Background())
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("revoked source remained public")
	}
	if p, e := s.page("handbook/README.md", false); e != nil || p.Text != "# Local" {
		t.Fatal("local availability lost")
	}
}

func TestGitHubCredentialsRedactedAndAppTokensScoped(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/a.md": "# A"})
	account, e := s.saveAccount(context.Background(), f.Client(), "operator", github.Secret{Kind: "token", Token: githubtest.Access})
	if e != nil || account.SecretRef != "" {
		t.Fatal(account, e)
	}
	state, e := os.ReadFile(path.Join(s.Dir(), "state.json"))
	if e != nil || strings.Contains(string(state), githubtest.Access) {
		t.Fatal("credential stored in article transaction")
	}
	metadata, _ := json.Marshal(s.accounts())
	if strings.Contains(string(metadata), githubtest.Access) || strings.Contains(string(metadata), "secret_ref") {
		t.Fatal("credential response leaked")
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	secret := github.Secret{Kind: "app", AppID: 7, InstallationID: 9, PrivateKey: string(encoded)}
	if login, e := f.Client().VerifyAccount(context.Background(), secret); e != nil || login != "org" {
		t.Fatal(login, e)
	}
	if _, e = f.Client().Token(context.Background(), &secret, "org/handbook", true); e != nil {
		t.Fatal(e)
	}
	jwt, e := github.AppJWT(secret)
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatal("invalid JWT")
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if e = rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature); e != nil {
		t.Fatal("JWT signature invalid", e)
	}
}
