package httpapi

import (
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScopedBrowserSessionIsolationAndRevocation(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		source(t, s, id, "private")
		if _, err := s.put(id, "guide.md", "# "+id+" secret", 0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.put(id, "guide.md", "# "+id+" current", 1); err != nil {
			t.Fatal(err)
		}
	}
	image := smallPNG(t)
	ref, err := domain.ImageRef(image)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(s.Dir(), "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Dir(), "blobs", ref.SHA), image, 0600); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	initial := s.Clone()
	for _, id := range []string{"alpha", "beta"} {
		src := initial.Sources[id]
		src.Assets = map[string]domain.BlobRef{"image.png": ref}
		initial.Sources[id] = src
	}
	err = s.Commit(initial)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.auth().CreateAccessKey("person", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", ReaderToken: "reader", Sessions: &auth.Sessions{}, AdminSessions: &auth.Sessions{}}
	call := func(method, path, body, origin string, cookie *http.Cookie, public bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		target := w
		target.Public = public
		target.ServeHTTP(out, r)
		return out
	}
	login := call("POST", "/api/session/login", fmt.Sprintf(`{"key":%q}`, key), "http://localhost:8080", nil, false)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatal("scoped login failed", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	if status := call("GET", "/api/session/status", "", "", cookie, false); !strings.Contains(status.Body.String(), `"authenticated":true`) {
		t.Fatal("valid scoped session reported unauthenticated")
	}
	for _, path := range []string{"/api/state", "/api/v1/sources", "/api/v1/articles", "/api/state?q=current", "/health"} {
		r := call("GET", path, "", "", cookie, false)
		if r.Code != 200 || strings.Contains(r.Body.String(), "beta") {
			t.Fatalf("scope leak %s: %d", path, r.Code)
		}
	}
	if call("GET", "/api/state?page=alpha%2Fguide.md", "", "", cookie, false).Code != 200 {
		t.Fatal("permitted article unavailable")
	}
	if call("GET", "/api/state?page=beta%2Fguide.md", "", "", cookie, false).Code != 404 {
		t.Fatal("other source disclosed")
	}
	if call("GET", "/media/alpha/image.png", "", "", cookie, false).Code != 200 || call("GET", "/media/beta/image.png", "", "", cookie, false).Code != 404 {
		t.Fatal("image scope not enforced")
	}
	history := call("GET", "/api/v1/articles?source_id=alpha&path=guide.md&history=1", "", "", cookie, false)
	if history.Code != 200 || strings.Contains(history.Body.String(), "alpha secret") || !strings.Contains(history.Body.String(), "alpha current") {
		t.Fatal("reader accessed historical revision or not current content")
	}
	if call("GET", "/api/state?page=alpha%2Fguide.md", "", "", cookie, true).Code != 404 {
		t.Fatal("public listener accepted session")
	}
	if call("POST", "/api/v1/articles", `{}`, "http://localhost:8080", cookie, false).Code != 403 {
		t.Fatal("reader session wrote article")
	}
	if call("GET", "/api/v1/github/accounts", "", "", cookie, false).Code != 404 {
		t.Fatal("reader session gained admin access")
	}
	s.Lock()
	db := s.Clone()
	k := db.AccessKeys["person"]
	k.Expires = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	db.AccessKeys["person"] = k
	err = s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if call("GET", "/api/state?page=alpha%2Fguide.md", "", "", cookie, false).Code != 404 {
		t.Fatal("expired key still reads private data")
	}
	s.Lock()
	db = s.Clone()
	k = db.AccessKeys["person"]
	k.Expires = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	db.AccessKeys["person"] = k
	err = s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if call("GET", "/api/state?page=alpha%2Fguide.md", "", "", cookie, false).Code != 200 {
		t.Fatal("valid scope not restored in fixture")
	}
	if err := s.auth().RevokeAccessKey("person"); err != nil {
		t.Fatal(err)
	}
	if call("GET", "/api/state?page=alpha%2Fguide.md", "", "", cookie, false).Code != 404 {
		t.Fatal("revoked session read private data")
	}
	if call("GET", "/media/alpha/image.png", "", "", cookie, false).Code != 404 {
		t.Fatal("revoked session read image")
	}
	status := call("GET", "/api/session/status", "", "", cookie, false)
	if !strings.Contains(status.Body.String(), `"authenticated":false`) {
		t.Fatal("revoked session reported active")
	}
}

func TestRevokedScopedSessionsDoNotExhaustSessionLimit(t *testing.T) {
	s := testStore(t)
	sessions := &auth.Sessions{}
	// 100 unexpired sessions scoped to a key digest the store does not hold.
	for i := 0; i < 100; i++ {
		_, err := sessions.Issue(auth.Login{
			Now:          time.Now().Add(time.Hour - 8*time.Hour),
			Authenticate: func() (auth.Grant, bool) { return auth.Grant{Scoped: true, Scope: [32]byte{1}}, true },
			Current:      func(auth.Grant) bool { return true },
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	w := Wiki{Store: s, ReaderToken: "reader", Sessions: sessions}
	r := httptest.NewRequest("POST", "http://localhost:8080/api/session/login", strings.NewReader(`{"key":"reader"}`))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "http://localhost:8080")
	out := httptest.NewRecorder()
	w.ServeHTTP(out, r)
	if active, scoped := sessions.Count(); out.Code != 200 || active != 1 || scoped != 0 {
		t.Fatal("revoked sessions occupied the session limit", out.Code)
	}
}
