package main

import (
	"encoding/json"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/render"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := openStore(filepath.Join(t.TempDir(), "data"))
	if e != nil {
		t.Fatal(e)
	}
	return s
}

// observedAt reads a source's last import observation time.
func observedAt(s *Store, sourceID string) time.Time {
	s.RLock()
	defer s.RUnlock()
	at, _ := s.Observed(sourceID)
	return at
}

func source(t *testing.T, s *Store, id, visibility string) {
	t.Helper()
	if e := s.addSource(Source{ID: id, Title: id, Kind: "wiki", Visibility: visibility}); e != nil {
		t.Fatal(e)
	}
}
func request(w Wiki, method, url, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	if auth {
		r.Header.Set("Authorization", "Bearer test-owner")
	}
	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, r)
	return rw
}

func TestReferrerPolicyOnPageAndError(t *testing.T) {
	w := Wiki{Store: testStore(t), Public: true, AllowedHosts: []string{"example.com"}}
	for _, target := range []string{"https://example.com/?key=synthetic", "https://other.example/"} {
		r := request(w, "GET", target, "", false)
		if got := r.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Fatalf("Referrer-Policy for %s = %q", target, got)
		}
	}
}

func TestDirectoryLinksResolveWithinVisibleSource(t *testing.T) {
	s := testStore(t)
	for _, item := range []struct{ id, visibility string }{{"alpha", "public"}, {"beta", "private"}} {
		source(t, s, item.id, item.visibility)
		for name, text := range map[string]string{"README.md": "# Root " + item.id, "docs/README.md": "# Preferred " + item.id, "docs/index.md": "# Index " + item.id, "index-only/index.md": "# Index only " + item.id, "guide.md": "# Guide\n\n[Docs](docs/)\n\n[Root](./)"} {
			if _, err := s.put(item.id, name, text, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	page, err := s.page("alpha/guide.md", false)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := render.Page(page)
	if err != nil || !strings.Contains(string(body), "page=alpha%2Fdocs") || !strings.Contains(string(body), "page=alpha%2F") {
		t.Fatal("directory links were not source qualified")
	}
	w := Wiki{Store: s, Token: "test-owner", Public: true}
	for _, tc := range []struct {
		path, want string
		status     int
	}{
		{"alpha/docs", "Preferred alpha", 200},
		{"alpha/docs/", "Preferred alpha", 200},
		{"alpha/docs/index.md", "Index alpha", 200},
		{"alpha/index-only", "Index only alpha", 200},
		{"alpha/", "Root alpha", 200},
		{"beta/docs", "", 404},
		{"alpha/missing", "", 404},
		{"alpha/../beta/docs", "", 404},
	} {
		r := request(w, "GET", "/api/state?page="+url.QueryEscape(tc.path), "", false)
		if r.Code != tc.status || (tc.want != "" && !strings.Contains(r.Body.String(), tc.want)) || strings.Contains(r.Body.String(), "Preferred beta") {
			t.Fatalf("directory resolution failed for %s: %d", tc.path, r.Code)
		}
	}
}
func TestPersistenceAndOptimisticConcurrency(t *testing.T) {
	s := testStore(t)
	source(t, s, "one", "private")
	if _, e := s.put("one", "guide.md", "# First", 0); e != nil {
		t.Fatal(e)
	}
	if _, e := s.put("one", "guide.md", "# Stale", 0); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.put("one", "guide.md", "# Second", 1); e != nil {
		t.Fatal(e)
	}
	reopened, e := openStore(s.Dir())
	if e != nil {
		t.Fatal(e)
	}
	a, e := reopened.article("one", "guide.md", false)
	if e != nil || len(a.Versions) != 2 || a.Versions[0].Text != "# First" || a.Versions[1].Text != "# Second" {
		t.Fatal("history lost", e)
	}
}

func TestFailedPersistenceDoesNotPublishRevision(t *testing.T) {
	s := testStore(t)
	source(t, s, "one", "private")
	state := filepath.Join(s.Dir(), "state.json")
	backup := filepath.Join(s.Dir(), "before-test.json")
	if e := os.Rename(state, backup); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(state, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := s.put("one", "guide.md", "# Must not publish", 0); e == nil {
		t.Fatal("persistence failure accepted")
	}
	if len(s.snapshot(false).Pages) != 0 {
		t.Fatal("failed transaction visible")
	}
	if e := os.Remove(state); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(backup, state); e != nil {
		t.Fatal(e)
	}
	if _, e := s.put("one", "guide.md", "# Retry", 0); e != nil {
		t.Fatal(e)
	}
}

func TestCorruptStoredRevisionIsRefused(t *testing.T) {
	s := testStore(t)
	source(t, s, "one", "private")
	if _, e := s.put("one", "guide.md", "# Original", 0); e != nil {
		t.Fatal(e)
	}
	article := s.DB().Articles["one/guide.md"]
	article.Versions[0].Text = "# Tampered"
	s.DB().Articles["one/guide.md"] = article
	b, e := json.Marshal(*s.DB())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.Dir(), "state.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = openStore(s.Dir()); e == nil {
		t.Fatal("corrupt revision accepted")
	}
}
func TestConcurrentSingleWinner(t *testing.T) {
	s := testStore(t)
	source(t, s, "one", "private")
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.put("one", "guide.md", "# Revision", 0); results <- e }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else if !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	if successes != 1 {
		t.Fatal(successes)
	}
}
func TestVisibilityAcrossEveryReadSurface(t *testing.T) {
	s := testStore(t)
	source(t, s, "open", "public")
	source(t, s, "closed", "private")
	s.put("open", "README.md", "# Shared", 0)
	s.put("closed", "README.md", "# Hidden canary", 0)
	w := Wiki{Store: s, Token: "test-owner"}
	for _, url := range []string{"/api/state", "/api/state?q=Hidden", "/api/v1/articles", "/api/v1/sources", "/api/v1/articles?source_id=closed&path=README.md", "/api/state?page=closed/README.md"} {
		rw := request(w, "GET", url, "", false)
		if strings.Contains(rw.Body.String(), "Hidden canary") || strings.Contains(rw.Body.String(), "closed") {
			t.Fatalf("private disclosure at %s: %s", url, rw.Body.String())
		}
	}
	if rw := request(w, "GET", "/api/state?page=closed/README.md", "", true); rw.Code != 200 || !strings.Contains(rw.Body.String(), "Hidden canary") {
		t.Fatal("owner cannot read")
	}
	w.Public = true
	if rw := request(w, "GET", "/api/state?page=closed/README.md", "", true); rw.Code != 404 {
		t.Fatal("public listener accepted owner token", rw.Code)
	}
}
func TestRESTWriteAuthorizationAndRevision(t *testing.T) {
	s := testStore(t)
	w := Wiki{Store: s, Token: "test-owner"}
	src := `{"id":"one","title":"One","kind":"wiki","visibility":"private"}`
	if r := request(w, "POST", "/api/v1/sources", src, false); r.Code != 403 {
		t.Fatal(r.Code)
	}
	if r := request(w, "POST", "/api/v1/sources", src, true); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	body := `{"source_id":"one","path":"README.md","text":"# Example","expected_revision":0}`
	if r := request(w, "POST", "/api/v1/articles", body, true); r.Code != 201 {
		t.Fatal(r.Code, r.Body)
	}
	if r := request(w, "POST", "/api/v1/articles", body, true); r.Code != 409 {
		t.Fatal(r.Code)
	}
	if r := request(w, "PUT", "/api/v1/articles", `{"source_id":"one","path":"README.md","text":"# Bad"}`, true); r.Code != 400 {
		t.Fatal(r.Code)
	}
	r := httptest.NewRequest("POST", "/api/v1/sources", strings.NewReader(src))
	r.Header.Set("Authorization", "Bearer test-owner")
	r.Header.Set("Origin", "http://localhost")
	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, r)
	if rw.Code != 403 {
		t.Fatal("browser write accepted")
	}
	w.Public = true
	if r := request(w, "POST", "/api/v1/sources", src, true); r.Code != 403 {
		t.Fatal(r.Code)
	}
}
func TestSourcesStaySeparateAndPathsBounded(t *testing.T) {
	s := testStore(t)
	source(t, s, "one", "public")
	source(t, s, "two", "private")
	s.put("one", "same.md", "# One", 0)
	s.put("two", "same.md", "# Two", 0)
	if p, e := s.page("one/same.md", false); e != nil || p.Text != "# One" {
		t.Fatal(e)
	}
	if p, e := s.page("two/same.md", false); e != nil || p.Text != "# Two" {
		t.Fatal(e)
	}
	for _, p := range []string{"../secret.md", "/etc/file.md", "a/../b.md", ".hidden/x.md", "x\\a.md", "x.txt"} {
		if _, e := s.put("one", p, "bad", 0); e == nil {
			t.Fatal(p)
		}
	}
	if e := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.put("program", "README.md", "no", 0); e == nil {
		t.Fatal("program root imported")
	}
	if _, e := s.put("program", "docs/guide.md", "yes", 0); e != nil {
		t.Fatal(e)
	}
}
func TestPublicFreshnessDoesNotLeakPrivateWrites(t *testing.T) {
	s := testStore(t)
	source(t, s, "one", "public")
	source(t, s, "two", "private")
	s.put("one", "a.md", "# A", 0)
	before := s.snapshot(true).Generated
	s.put("two", "secret.md", "# Secret", 0)
	if s.snapshot(true).Generated != before {
		t.Fatal("private update timestamp disclosed")
	}
}
func TestHostGuardAndUnknownFields(t *testing.T) {
	s := testStore(t)
	w := Wiki{Store: s, Token: "test-owner", AllowedHosts: []string{"localhost"}}
	if r := request(w, "GET", "http://evil.invalid/api/state", "", true); r.Code != 403 {
		t.Fatal(r.Code)
	}
	w.AllowedHosts = nil
	rw := request(w, "POST", "/api/v1/sources", `{"id":"one","title":"One","kind":"wiki","visibility":"public","unexpected":true}`, true)
	if rw.Code != 400 {
		t.Fatal(rw.Code)
	}
	rw = request(w, "GET", "/api/state", "", false)
	var response map[string]any
	if e := json.Unmarshal(rw.Body.Bytes(), &response); e != nil {
		t.Fatal(e)
	}
}
