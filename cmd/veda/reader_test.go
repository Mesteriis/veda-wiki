package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnlyCredentialBoundaries(t *testing.T) {
	s := testStore(t)
	source(t, s, "private", "private")
	if _, err := s.put("private", "guide.md", "# Secret version 1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("private", "guide.md", "# Secret version 2", 1); err != nil {
		t.Fatal(err)
	}
	a := s.DB().Articles["private/guide.md"]
	a.Published = &a.Versions[0]
	s.DB().Articles["private/guide.md"] = a
	w := Wiki{Store: s, Token: "owner", ReaderToken: "read-only"}
	call := func(method, url, token, body string, public bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		target := w
		target.Public = public
		target.ServeHTTP(recorder, r)
		return recorder
	}
	got := call("GET", "/api/v1/articles?source_id=private&path=guide.md&history=1", "read-only", "", false)
	if got.Code != 200 {
		t.Fatal(got.Code)
	}
	var received Article
	if err := json.Unmarshal(got.Body.Bytes(), &received); err != nil || len(received.Versions) != 1 || received.Versions[0].Text != "# Secret version 2" || received.Published != nil {
		t.Fatal("reader leaked history or missed latest version", err)
	}
	if got := call("GET", "/api/state?page=private%2Fguide.md", "read-only", "", false); got.Code != 200 || !strings.Contains(got.Body.String(), "Secret version 2") {
		t.Fatal("reader cannot read private view", got.Code)
	}
	if got := call("GET", "/api/v1/articles?source_id=private&path=guide.md", "read-only", "", true); got.Code != 404 {
		t.Fatal("public listener accepted reader", got.Code)
	}
	if got := call("POST", "/api/v1/articles", "read-only", `{"source_id":"private","path":"x.md","text":"# x","expected_revision":0}`, false); got.Code != 403 {
		t.Fatal("reader can write", got.Code)
	}
	for _, url := range []string{"/api/v1/conflicts", "/api/v1/github/accounts", "/api/v1/github/connections"} {
		if got := call("GET", url, "read-only", "", false); got.Code != 404 {
			t.Fatalf("reader reached admin %s: %d", url, got.Code)
		}
	}
}

func TestGeneratedReaderKeyPersistsAndIsPrivate(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateKey(dir, "reader.key")
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateKey(dir, "reader.key")
	if err != nil || string(first) != string(second) {
		t.Fatal("reader key changed", err)
	}
	info, err := os.Stat(filepath.Join(dir, "reader.key"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("reader key permissions", err)
	}
	if err := os.Chmod(filepath.Join(dir, "reader.key"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateKey(dir, "reader.key"); err == nil {
		t.Fatal("insecure reader key accepted")
	}
}
