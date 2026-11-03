package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func TestMultiSourceVisibilityAndOutageAreIndependentAfterSQLiteRestart(t *testing.T) {
	s := testStore(t)
	first := githubtest.New(t, map[string]string{"docs/guide.md": "# First public canary"})
	second := githubtest.New(t, map[string]string{"docs/guide.md": "# Second public canary"})
	second.Modify(func() { second.Name = "org/second"; second.RepoID = 42 })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			second.Serve(w, r)
		} else {
			first.Serve(w, r)
		}
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	manager := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []struct{ id, name string }{{"one", "org/handbook"}, {"two", "org/second"}} {
		if _, err := manager.Connect(context.Background(), connectRequest{ID: repo.id, Repository: repo.name, AccountID: "operator"}); err != nil {
			t.Fatal(err)
		}
	}
	manager.Once(context.Background())
	if len(s.snapshot(true).Pages) != 2 {
		t.Fatal("initial sources not public")
	}
	if err := migrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	manager = newSyncManager(s, client)
	first.Modify(func() { first.Private = true })
	if err = s.retryConnection("one"); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if _, err = s.page("one/docs/guide.md", true); err != ErrNotFound {
		t.Fatal("newly private source remained public", err)
	}
	if page, err := s.page("two/docs/guide.md", true); err != nil || page.Text != "# Second public canary" {
		t.Fatal("other source lost public access", err)
	}
	if page, err := s.page("one/docs/guide.md", false); err != nil || page.Text != "# First public canary" {
		t.Fatal("local private copy lost", err)
	}
	w := Wiki{Store: s, Public: true}
	for _, route := range []string{"/api/state", "/api/v1/sources", "/api/v1/articles", "/api/v1/search?q=canary"} {
		out := request(w, "GET", route, "", false)
		if out.Code != 200 || strings.Contains(out.Body.String(), "First public canary") || strings.Contains(out.Body.String(), "one/docs/guide.md") {
			t.Fatal("private source leaked publicly", route, out.Code)
		}
	}
	second.Modify(func() { second.Fail = true })
	if err = s.retryConnection("two"); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("unverified source remained public")
	}
	if page, err := s.page("two/docs/guide.md", false); err != nil || page.Text != "# Second public canary" {
		t.Fatal("outage removed local copy", err)
	}
	restored, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.snapshot(true).Pages) != 0 {
		t.Fatal("visibility failure lost on reopen")
	}
	second.Modify(func() { second.Fail = false })
	if err = restored.retryConnection("two"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(restored, client).Once(context.Background())
	if page, err := restored.page("two/docs/guide.md", true); err != nil || page.Text != "# Second public canary" {
		t.Fatal("verified public source did not recover", err)
	}
	if _, err = restored.page("one/docs/guide.md", true); err != ErrNotFound {
		t.Fatal("other private source was republished", err)
	}
}

func TestInvalidDocsEntryDoesNotAffectOtherRepository(t *testing.T) {
	s := testStore(t)
	first := githubtest.New(t, map[string]string{"docs/guide.md": "# First source"})
	second := githubtest.New(t, map[string]string{"docs/guide.md": "# Second source"})
	second.Modify(func() { second.Name = "org/second"; second.RepoID = 42 })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			second.Serve(w, r)
		} else {
			first.Serve(w, r)
		}
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	manager := newSyncManager(s, client)
	for _, repo := range []struct{ id, name string }{{"one", "org/handbook"}, {"two", "org/second"}} {
		if _, err := manager.Connect(context.Background(), connectRequest{ID: repo.id, Repository: repo.name}); err != nil {
			t.Fatal(err)
		}
	}
	manager.Once(context.Background())
	if err := migrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	otherGeneration := s.DB().Sources["two"].ImportGeneration
	first.Advance(map[string]string{"docs": "not a directory"})
	newSyncManager(s, client).Once(context.Background())
	if _, err := s.page("one/docs/guide.md", true); err != ErrNotFound {
		t.Fatal("invalid source remained public", err)
	}
	if page, err := s.page("one/docs/guide.md", false); err != nil || page.Text != "# First source" {
		t.Fatal("invalid source lost local article", err)
	}
	if page, err := s.page("two/docs/guide.md", true); err != nil || page.Text != "# Second source" {
		t.Fatal("other repository affected", err)
	}
	if s.DB().Sources["two"].ImportGeneration != otherGeneration {
		t.Fatal("other source generation changed")
	}
	out := request(Wiki{Store: s, Public: true}, "GET", "/api/v1/search?q=source", "", false)
	if out.Code != 200 || strings.Contains(out.Body.String(), "First source") || !strings.Contains(out.Body.String(), "Second source") {
		t.Fatal("merged public search violated isolation", out.Code)
	}
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.page("one/docs/guide.md", true); err != ErrNotFound {
		t.Fatal("invalid source exposed after restart", err)
	}
	if _, err := s.page("two/docs/guide.md", true); err != nil {
		t.Fatal("other source unavailable after restart", err)
	}
	first.Advance(map[string]string{"docs/guide.md": "# First source"})
	if err := s.retryConnection("one"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(s, client).Once(context.Background())
	if _, err := s.page("one/docs/guide.md", true); err != nil {
		t.Fatal("corrected source did not recover", err)
	}
}
