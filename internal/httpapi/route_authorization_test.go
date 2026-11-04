package httpapi

import (
	"github.com/Mesteriis/veda-wiki/internal/auth"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Every mutating API route must refuse non-owner credentials on its own, not
// only through the shared guard at the top of api().
func TestMutatingRoutesRefuseNonOwners(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	scoped, err := s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	article := `{"source_id":"alpha","path":"new.md","text":"# New","expected_revision":0}`
	routes := []struct{ method, path, body string }{
		{"POST", "/api/v1/articles", article},
		{"PUT", "/api/v1/articles", article},
		{"POST", "/api/v1/sources", `{"id":"beta","title":"Beta","kind":"wiki","visibility":"public"}`},
		{"POST", "/api/v1/articles/copy", `{}`},
		{"POST", "/api/v1/sources/reconcile", `{}`},
		{"POST", "/api/v1/sources/mirrors", `{}`},
		{"POST", "/api/v1/moves", `{}`},
		{"POST", "/api/v1/moves/cancel", `{}`},
		{"POST", "/api/v1/conflicts/resolve", `{}`},
		{"POST", "/api/v1/import", `{}`},
		{"POST", "/api/v1/access-keys", `{}`},
		{"POST", "/api/v1/access-keys/revoke", `{"id":"agent"}`},
		{"POST", "/api/v1/users", `{}`},
		{"POST", "/api/v1/teams", `{}`},
		{"POST", "/api/v1/github/accounts", `{}`},
		{"POST", "/api/v1/github/connections", `{}`},
		{"POST", "/api/v1/github/sync", `{}`},
		{"POST", "/api/v1/github/publish-job", `{}`},
		{"POST", "/api/v1/github/cancel-bundle", `{}`},
		{"POST", "/api/v1/knowledge/reindex", `{"scope":"all"}`},
	}
	credentials := map[string]struct {
		header string
		local  bool
	}{
		"anonymous":    {},
		"reader key":   {header: "Bearer test-reader"},
		"scoped key":   {header: "Bearer " + scoped},
		"local socket": {local: true},
	}
	before := s.state(t).Updated
	for name, credential := range credentials {
		for _, route := range routes {
			w := Wiki{Store: s, Token: "test-owner", ReaderToken: "test-reader", ReaderLocal: credential.local}
			r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			if credential.header != "" {
				r.Header.Set("Authorization", credential.header)
			}
			out := httptest.NewRecorder()
			w.ServeHTTP(out, r)
			if out.Code < 400 {
				t.Errorf("%s %s accepted %s: %d", route.method, route.path, name, out.Code)
			}
		}
	}
	if s.state(t).Updated != before {
		t.Fatal("a non-owner request changed the store")
	}
	// The same direct call with owner=false must refuse even if the shared
	// guard is bypassed by a future caller that forwards a non-GET request.
	for _, route := range routes {
		r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
		out := httptest.NewRecorder()
		Wiki{Store: s}.handler().routeAPI(out, r, false, true)
		if out.Code < 400 {
			t.Errorf("%s %s accepted a reader without the shared guard: %d", route.method, route.path, out.Code)
		}
	}
	if s.state(t).Updated != before {
		t.Fatal("a reader call without the shared guard changed the store")
	}
}

// Every route keeps the role it had before the handlers moved: browser
// administration answers 404 without its session cookie even to the owner's
// bearer token, owner-only API routes answer 404 to every other credential
// and work for the owner, reader routes are hidden from anonymous callers,
// and a scoped key's state never shows another source.
func TestEveryRouteKeepsItsRole(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	if _, err := s.put("alpha", "guide.md", "# Alpha guide", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("beta", "secret.md", "# Beta secret", 0); err != nil {
		t.Fatal(err)
	}
	scoped, err := s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	call := func(w Wiki, method, path, header string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	private := Wiki{Store: s, Token: "test-owner", ReaderToken: "test-reader", Sessions: &auth.Sessions{}, AdminSessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}}
	public := private
	public.Public = true
	credentials := map[string]string{"anonymous": "", "owner key": "Bearer test-owner", "reader key": "Bearer test-reader", "scoped key": "Bearer " + scoped}
	browserAdmin := []string{"/api/access/keys", "/api/access/sources", "/api/access/users", "/api/access/teams", "/api/admin/github/accounts", "/api/admin/github/connections", "/api/admin/github/mirrors"}
	for name, header := range credentials {
		for _, listener := range []Wiki{private, public} {
			for _, path := range browserAdmin {
				if got := call(listener, "GET", path, header).Code; got != 404 {
					t.Errorf("GET %s without an admin cookie (%s, public=%v): %d", path, name, listener.Public, got)
				}
			}
		}
	}
	ownerOnly := []string{"/api/v1/access-keys", "/api/v1/users", "/api/v1/teams", "/api/v1/moves", "/api/v1/conflicts", "/api/v1/sources/mirrors", "/api/v1/github/accounts", "/api/v1/github/connections", "/api/v1/github/outbox", "/api/v1/knowledge/status"}
	for _, path := range ownerOnly {
		for name, header := range credentials {
			got := call(private, "GET", path, header).Code
			if name == "owner key" && got == 404 {
				t.Errorf("owner lost GET %s", path)
			}
			if name != "owner key" && got != 404 {
				t.Errorf("GET %s reached by %s: %d", path, name, got)
			}
			if got := call(public, "GET", path, header).Code; got != 404 {
				t.Errorf("public listener served GET %s to %s: %d", path, name, got)
			}
		}
	}
	readerOnly := []string{"/api/v1/articles/proposal-base?source_id=alpha&path=guide.md", "/api/v1/articles/new-proposal-base?source_id=alpha&path=new.md"}
	for _, path := range readerOnly {
		if got := call(private, "GET", path, "").Code; got != 404 {
			t.Errorf("anonymous GET %s: %d", path, got)
		}
		if got := call(public, "GET", path, "Bearer test-owner").Code; got != 404 {
			t.Errorf("public listener GET %s with owner key: %d", path, got)
		}
		if got := call(private, "GET", path, "Bearer test-reader").Code; got == 404 {
			t.Errorf("reader lost GET %s", path)
		}
	}
	state := call(private, "GET", "/api/state", "Bearer "+scoped)
	if state.Code != 200 || !strings.Contains(state.Body.String(), "alpha/guide.md") || strings.Contains(state.Body.String(), "beta") {
		t.Fatalf("scoped state showed another source: %d %s", state.Code, state.Body.String())
	}
	if got := call(private, "GET", "/api/state?page=beta%2Fsecret.md", "Bearer "+scoped).Code; got != 404 {
		t.Fatalf("scoped key read another source's page: %d", got)
	}
	if got := call(private, "GET", "/api/v1/articles?source_id=beta&path=secret.md", "Bearer "+scoped).Code; got != 404 {
		t.Fatalf("scoped key read another source's article: %d", got)
	}
}
