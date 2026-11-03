package main

import (
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
	scoped, err := s.createAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	article := `{"source_id":"alpha","path":"new.md","text":"# New","expected_revision":0}`
	routes := []struct{ method, path, body string }{
		{"POST", "/api/v1/articles", article},
		{"PUT", "/api/v1/articles", article},
		{"POST", "/api/v1/sources", `{"id":"beta","title":"Beta","kind":"wiki","visibility":"public"}`},
		{"POST", "/api/v1/articles/copy", `{}`},
		{"POST", "/api/v1/articles/translation", `{}`},
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
	before := s.DB().Updated
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
	if s.DB().Updated != before {
		t.Fatal("a non-owner request changed the store")
	}
	// The same direct call with owner=false must refuse even if the shared
	// guard is bypassed by a future caller that forwards a non-GET request.
	for _, route := range routes {
		r := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
		out := httptest.NewRecorder()
		Wiki{Store: s}.routeAPI(out, r, false, true)
		if out.Code < 400 {
			t.Errorf("%s %s accepted a reader without the shared guard: %d", route.method, route.path, out.Code)
		}
	}
	if s.DB().Updated != before {
		t.Fatal("a reader call without the shared guard changed the store")
	}
}
