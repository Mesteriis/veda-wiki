package main

import (
	"context"
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)


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
	if s.auth().AccessScope(out.Key, time.Now()) != nil {
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
	if err := os.MkdirAll(filepath.Join(s.Dir(), "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "blobs", ref.SHA), image, 0600); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	db := s.Clone()
	for _, id := range []string{"alpha", "beta"} {
		src := db.Sources[id]
		src.Assets = map[string]BlobRef{"image.png": ref}
		db.Sources[id] = src
	}
	err = s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
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
	if err := s.auth().RevokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	if got := call("GET", "/api/v1/articles?source_id=alpha&path=guide.md", false); got.Code != 404 {
		t.Fatal("revoked key still reads private content", got.Code)
	}
}

func TestReaderSocketScopedKeyCannotFallBackAfterRevocation(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	for _, id := range []string{"alpha", "beta"} {
		if _, err := s.put(id, "guide.md", "# "+id, 0); err != nil {
			t.Fatal(err)
		}
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	call := func(key string, required bool, sourceID string) int {
		r := httptest.NewRequest("GET", "/api/v1/articles?source_id="+sourceID+"&path=guide.md", nil)
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		out := httptest.NewRecorder()
		(Wiki{Store: s, ReaderLocal: true, ReaderKeyRequired: required}).ServeHTTP(out, r)
		return out.Code
	}
	if call("", false, "beta") != 200 || call("", true, "beta") != 404 {
		t.Fatal("socket key requirement failed")
	}
	if call(key, true, "alpha") != 200 || call(key, true, "beta") != 404 {
		t.Fatal("socket scope not applied")
	}
	if call(strings.Repeat("0", 64), false, "beta") != 404 {
		t.Fatal("invalid explicit key fell back to socket access")
	}
	if err := s.auth().RevokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	if call(key, true, "alpha") != 404 || call(key, false, "alpha") != 404 {
		t.Fatal("revoked key fell back to socket access")
	}
}


func TestGitHubSourceMovePreservesVersion4AndAccessKeys(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	connectFixture(t, s, f, true)
	key, err := s.auth().CreateAccessKey("agent", []string{"handbook"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatal("move did not start", err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.DB().Version != 4 || len(reopened.auth().AccessScope(key, time.Now())) != 1 {
		t.Fatal("move downgraded or lost scoped access", err)
	}
}

func TestAccessKeyUpgradePreservesPendingGitHubMove(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	_ = connectFixture(t, s, f, true)
	_, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil || s.DB().Version != 3 {
		t.Fatal("version 3 move not created", err)
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"local"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.DB().Version != 4 || len(reopened.auth().AccessScope(key, time.Now())) != 1 {
		t.Fatal("version upgrade lost access", err)
	}
	manager := newSyncManager(reopened, f.Client())
	manager.Once(context.Background())
	manager.Once(context.Background())
	if reopened.moves()[0].State != "complete" || len(reopened.auth().AccessScope(key, time.Now())) != 1 {
		t.Fatal("move or access lost after upgrade")
	}
}

func TestTranslationDraftCannotModifySourceDuringPreparedMove(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Setup"})
	manager := connectFixture(t, s, f, true)
	a, err := s.article("handbook", "docs/guide.md", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "docs/guide.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "guide.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if s.moves()[0].State != "deleting_source" {
		t.Fatal("deletion not prepared")
	}
	_, err = s.saveTranslation(translationRequest{SourceID: "handbook", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: a.Versions[0].SHA, Text: "# Установка"}, true)
	if err != ErrConflict {
		t.Fatal("translation modified source during prepared move", err)
	}
}
