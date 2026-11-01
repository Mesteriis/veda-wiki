package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccessKeyScopePersistenceExpiryAndRevocation(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	expiry := time.Now().Add(time.Hour)
	key, err := s.createAccessKey("agent", []string{"alpha"}, expiry)
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil || strings.Contains(string(state), key) {
		t.Fatal("bearer token was stored", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Version != 4 {
		t.Fatal("key store did not persist", err)
	}
	scope := reopened.accessScope(key, time.Now())
	if len(scope) != 1 || scope[0] != "alpha" {
		t.Fatal("wrong scope")
	}
	if reopened.accessScope(key, expiry.Add(time.Second)) != nil {
		t.Fatal("expired key accepted")
	}
	if reopened.accessScope(strings.Repeat("0", 64), time.Now()) != nil {
		t.Fatal("invalid key accepted")
	}
	if err := reopened.revokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	if reopened.accessScope(key, time.Now()) != nil {
		t.Fatal("revoked key accepted")
	}
	reopened, err = openStore(s.dir)
	if err != nil || reopened.accessScope(key, time.Now()) != nil {
		t.Fatal("revocation not durable", err)
	}
}

func TestAccessKeyAdministrationRequiresOwnerAndDoesNotListSecrets(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	w := Wiki{Store: s, Token: "test-owner", ReaderToken: "reader"}
	body := `{"id":"codex","sources":["alpha"],"expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano) + `"}`
	if got := request(w, "POST", "/api/v1/access-keys", body, false); got.Code != 403 {
		t.Fatal("unauthorized creation", got.Code)
	}
	created := request(w, "POST", "/api/v1/access-keys", body, true)
	var out struct {
		Key string `json:"key"`
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &out) != nil || len(out.Key) != 64 {
		t.Fatal("key creation failed", created.Code)
	}
	listed := request(w, "GET", "/api/v1/access-keys", "", true)
	if listed.Code != 200 || strings.Contains(listed.Body.String(), out.Key) || strings.Contains(listed.Body.String(), "hash") {
		t.Fatal("key list exposed secret material", listed.Code)
	}
	if got := request(w, "GET", "/api/v1/access-keys", "", false); got.Code != 404 {
		t.Fatal("key list is public", got.Code)
	}
	if got := request(w, "POST", "/api/v1/access-keys/revoke", `{"id":"codex"}`, true); got.Code != 200 {
		t.Fatal("key revocation failed", got.Code)
	}
	if s.accessScope(out.Key, time.Now()) != nil {
		t.Fatal("revoked key accepted")
	}
}

func TestScopedReaderCannotCrossSourceBoundaries(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	for _, id := range []string{"alpha", "beta"} {
		if _, err := s.put(id, "AGENTS.md", "# "+id+" instructions", 0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put(id, "guide.md", "# "+id+" secretmarker", 0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put(id, "guide.md", "# "+id+" currentmarker", 1); err != nil {
			t.Fatal(err)
		}
	}
	image := smallPNG(t)
	ref, err := imageRef(image)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "blobs", ref.SHA), image, 0600); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	db := s.clone()
	for _, id := range []string{"alpha", "beta"} {
		src := db.Sources[id]
		src.Assets = map[string]BlobRef{"image.png": ref}
		db.Sources[id] = src
	}
	err = s.commit(db)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.createAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wiki := Wiki{Store: s, Token: "owner", ReaderToken: "reader"}
	call := func(method, path string, public bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		w := wiki
		w.Public = public
		w.ServeHTTP(out, r)
		return out
	}
	for _, path := range []string{"/api/v1/sources", "/api/v1/articles", "/api/state?q=currentmarker", "/health"} {
		r := call("GET", path, false)
		if r.Code != 200 || strings.Contains(r.Body.String(), "beta") {
			t.Fatalf("scope leaked through %s: %d", path, r.Code)
		}
	}
	for _, path := range []string{"/api/v1/articles?source_id=beta&path=guide.md", "/api/v1/articles?source_id=beta&path=AGENTS.md", "/api/state?page=beta%2Fguide.md", "/media/beta/image.png", "/api/v1/github/accounts", "/api/v1/conflicts"} {
		if got := call("GET", path, false); got.Code != 404 {
			t.Fatalf("restricted path %s: %d", path, got.Code)
		}
	}
	got := call("GET", "/api/v1/articles?source_id=alpha&path=guide.md&history=1", false)
	if got.Code != 200 || !strings.Contains(got.Body.String(), "currentmarker") || strings.Contains(got.Body.String(), "secretmarker") {
		t.Fatal("reader got history or no current article", got.Code)
	}
	if got := call("GET", "/media/alpha/image.png", false); got.Code != 200 {
		t.Fatal("allowed image unavailable", got.Code)
	}
	if got := call("POST", "/api/v1/articles", false); got.Code != 403 {
		t.Fatal("scope may write", got.Code)
	}
	if got := call("GET", "/api/v1/articles?source_id=alpha&path=guide.md", true); got.Code != 404 {
		t.Fatal("public listener accepted private scope", got.Code)
	}
	if err := s.revokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	if got := call("GET", "/api/v1/articles?source_id=alpha&path=guide.md", false); got.Code != 404 {
		t.Fatal("revoked key still reads private content", got.Code)
	}
}

func TestAccessKeyCreationRejectsInvalidScope(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	for _, scope := range [][]string{nil, {"missing"}, {"alpha", "alpha"}} {
		if _, err := s.createAccessKey("agent", scope, time.Now().Add(time.Hour)); err != ErrInvalid {
			t.Fatalf("scope accepted: %v", err)
		}
	}
	if _, err := s.createAccessKey("agent", []string{"alpha"}, time.Now().Add(-time.Second)); err != ErrInvalid {
		t.Fatal("expired key created", err)
	}
	if s.db.Version != 1 || len(s.db.AccessKeys) != 0 {
		t.Fatal("invalid request mutated store")
	}
}
