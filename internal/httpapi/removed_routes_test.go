package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
)

// The removed translation routes answer exactly like a route that never
// existed: 404 to the owner, and the same status and body as an unknown route
// to every other credential and listener.
func TestRemovedTranslationRoutesAnswerAsUnknown(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "alpha", "private")
	if _, err := s.put("alpha", "docs/guide.md", "# Guide", 0); err != nil {
		t.Fatal(err)
	}
	scoped, err := s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	private := Wiki{Store: s, Token: "test-owner", ReaderToken: "test-reader", Sessions: &auth.Sessions{}, AdminSessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}}
	public := private
	public.Public = true
	call := func(w Wiki, method, path, header string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(`{"source_id":"alpha","path":"docs/guide.md"}`))
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	removed := []string{"/api/v1/articles/display", "/api/v1/articles/display-excerpt", "/api/v1/articles/translation", "/api/v1/articles/translation-validate", "/api/v1/articles/translation-needed"}
	unknown := "/api/v1/articles/never-existed"
	query := "?source_id=alpha&path=docs%2Fguide.md"
	credentials := map[string]string{"anonymous": "", "owner key": "Bearer test-owner", "reader key": "Bearer test-reader", "scoped key": "Bearer " + scoped}
	before := s.state(t).Updated
	for name, header := range credentials {
		for _, listener := range []Wiki{private, public} {
			for _, method := range []string{"GET", "POST"} {
				want := call(listener, method, unknown+query, header)
				for _, path := range removed {
					got := call(listener, method, path+query, header)
					if got.Code != want.Code || got.Body.String() != want.Body.String() {
						t.Errorf("%s %s (%s, public=%v): %d %q, unknown route: %d %q", method, path, name, listener.Public, got.Code, got.Body.String(), want.Code, want.Body.String())
					}
					if name == "owner key" && !listener.Public && got.Code != 404 {
						t.Errorf("owner %s %s: %d, want 404", method, path, got.Code)
					}
				}
			}
		}
	}
	if s.state(t).Updated != before {
		t.Fatal("a removed route changed the store")
	}
}
