package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSkippedProgramRechecksVisibilityAndImportsNewDocs(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"README.md": "# Project", "src/main.go": "package main"})
	var treeCalls, repoCalls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/trees/") {
			treeCalls.Add(1)
		}
		if r.URL.Path == "/repos/org/handbook" {
			repoCalls.Add(1)
		}
		f.serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, &GitHubClient{base: gateway.URL, http: gateway.Client()})
	if _, err := m.connect(context.Background(), connectRequest{ID: "program", Repository: "org/handbook"}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if c := s.connections()[0]; c.State != "skipped" || c.LastPull == "" || c.TreeSHA == "" {
		t.Fatal("skip was not recorded", c.State)
	}
	if len(s.snapshot(false).Pages) != 0 {
		t.Fatal("imported a repository without docs")
	}
	firstTree, firstRepo := treeCalls.Load(), repoCalls.Load()
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	m = newSyncManager(reopened, &GitHubClient{base: gateway.URL, http: gateway.Client()})
	m.Once(context.Background())
	if treeCalls.Load() != firstTree || repoCalls.Load() != firstRepo+1 {
		t.Fatal("unchanged skip did not reuse tree or recheck visibility")
	}
	f.advance(map[string]string{"docs/guide.md": "# New documentation"})
	m.Once(context.Background())
	if treeCalls.Load() != firstTree+1 {
		t.Fatal("changed head did not reload tree")
	}
	if _, err = reopened.page("program/docs/guide.md", true); err != nil {
		t.Fatal("new documentation not imported", err)
	}
}
