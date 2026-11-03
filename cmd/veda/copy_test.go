package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCopyArticlePublicationAndProvenance(t *testing.T) {
	s := testStore(t)
	source(t, s, "private", "private")
	source(t, s, "public", "public")
	if _, e := s.put("private", "nested/guide.md", "# Private fixture", 0); e != nil {
		t.Fatal(e)
	}
	req := copyRequest{SourceID: "private", SourcePath: "nested/guide.md", ExpectedRevision: 1, DestinationID: "public", DestinationPath: "guide.md"}
	if _, e := s.copyArticle(req); e != ErrInvalid {
		t.Fatal("private copy published without approval", e)
	}
	if _, e := s.article("public", "guide.md", true); e != ErrNotFound {
		t.Fatal("refused copy visible", e)
	}
	req.ConfirmPublication = true
	if _, e := s.copyArticle(req); e != nil {
		t.Fatal(e)
	}
	original, e := s.article("private", "nested/guide.md", false)
	if e != nil || original.Deleted {
		t.Fatal("source removed", e)
	}
	owner, e := s.article("public", "guide.md", false)
	if e != nil || owner.CopiedFrom == nil || owner.CopiedFrom.SourceID != "private" || owner.CopiedFrom.Revision != 1 {
		t.Fatal("missing provenance", e)
	}
	public, e := s.article("public", "guide.md", true)
	if e != nil || public.CopiedFrom != nil {
		t.Fatal("private provenance disclosed", e)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	response := request(w, "GET", "/api/v1/articles?source_id=public&path=guide.md", "", false)
	readerReq := httptest.NewRequest("GET", "/api/v1/articles?source_id=public&path=guide.md", nil)
	readerReq.Header.Set("Authorization", "Bearer read-only")
	readerResponse := httptest.NewRecorder()
	Wiki{Store: s, Token: "test-owner", ReaderToken: "read-only"}.ServeHTTP(readerResponse, readerReq)
	if strings.Contains(readerResponse.Body.String(), "copied_from") {
		t.Fatal("reader received owner-only provenance")
	}
	if strings.Contains(response.Body.String(), "nested/guide") || strings.Contains(response.Body.String(), "copied_from") {
		t.Fatal("public API leaked provenance")
	}
	if _, e := s.copyArticle(req); e != ErrConflict {
		t.Fatal("destination overwritten", e)
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := reopened.article("public", "guide.md", false)
	if e != nil || saved.CopiedFrom == nil || saved.CopiedFrom.SHA != owner.CopiedFrom.SHA {
		t.Fatal("provenance lost after restart", e)
	}
}
func TestCopyArticleChecksRevisionAndRejectsSecrets(t *testing.T) {
	s := testStore(t)
	source(t, s, "one", "private")
	source(t, s, "two", "public")
	if _, e := s.put("one", "a.md", "# Original", 0); e != nil {
		t.Fatal(e)
	}
	req := copyRequest{SourceID: "one", SourcePath: "a.md", ExpectedRevision: 2, DestinationID: "two", DestinationPath: "a.md", ConfirmPublication: true}
	if _, e := s.copyArticle(req); e != ErrConflict {
		t.Fatal("stale revision accepted", e)
	}
	if _, e := s.put("one", "secret.md", "password = fixture-secret-value", 0); e != nil {
		t.Fatal(e)
	}
	secret := copyRequest{SourceID: "one", SourcePath: "secret.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "secret.md", ConfirmPublication: true}
	if _, e := s.copyArticle(secret); e != ErrInvalid {
		t.Fatal("secret-like content published", e)
	}
	req.ExpectedRevision = 1
	req.DestinationPath = "../a.md"
	if _, e := s.copyArticle(req); e != ErrInvalid {
		t.Fatal("invalid destination accepted", e)
	}
}
func TestCopyQueuesAndSynchronizesDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "private", "private")
	s.put("private", "a.md", "# Private fixture", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	m := connectFixture(t, s, f, true)
	req := copyRequest{SourceID: "private", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "copied.md", ConfirmPublication: true}
	if _, e := s.copyArticle(req); e != nil {
		t.Fatal(e)
	}
	jobs := s.jobs()
	if len(jobs) != 1 || jobs[0].State != "pending" || jobs[0].Visibility != "public" {
		t.Fatal("copy not queued", jobs)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.contents()["copied.md"] != "# Private fixture" || s.jobs()[0].State != "complete" {
		t.Fatal("copy did not synchronize", s.jobs())
	}
	if _, e := s.article("private", "a.md", false); e != nil {
		t.Fatal("copy removed source", e)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	data, _ := json.Marshal(req)
	if r := request(w, "POST", "/api/v1/articles/copy", string(data), false); r.Code != 403 {
		t.Fatal("anonymous copy accepted", r.Code)
	}
}

func TestCopyToUnverifiedPublicDestinationRequiresLaterApproval(t *testing.T) {
	s := testStore(t)
	source(t, s, "private", "private")
	if _, e := s.put("private", "a.md", "# unpublished fixture", 0); e != nil {
		t.Fatal(e)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	m := connectFixture(t, s, f, true)
	s.mu.Lock()
	c := s.db.Connections["handbook"]
	c.VerifiedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
	s.db.Connections[c.ID] = c
	s.mu.Unlock()
	req := copyRequest{SourceID: "private", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "copy.md"}
	if _, e := s.copyArticle(req); e != nil {
		t.Fatal(e)
	}
	if s.jobs()[0].Visibility != "private" {
		t.Fatal("unverified copy marked public")
	}
	m.Once(context.Background())
	if f.writeCount() != 0 || s.jobs()[0].LastError != "publication_confirmation_required" {
		t.Fatal("unverified copy auto-published", s.jobs())
	}
}
