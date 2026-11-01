package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
	}
	key, err := s.createAccessKey("person", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", ReaderToken: "reader", Sessions: &readerSessions{}, AdminSessions: &readerSessions{}}
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
	for _, path := range []string{"/api/state", "/api/v1/sources", "/api/v1/articles", "/health"} {
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
	if call("GET", "/api/state?page=alpha%2Fguide.md", "", "", cookie, true).Code != 404 {
		t.Fatal("public listener accepted session")
	}
	if call("POST", "/api/v1/articles", `{}`, "http://localhost:8080", cookie, false).Code != 403 {
		t.Fatal("reader session wrote article")
	}
	if call("GET", "/api/v1/github/accounts", "", "", cookie, false).Code != 404 {
		t.Fatal("reader session gained admin access")
	}
	s.mu.Lock()
	db := s.clone()
	k := db.AccessKeys["person"]
	k.Expires = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	db.AccessKeys["person"] = k
	err = s.commit(db)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if call("GET", "/api/state?page=alpha%2Fguide.md", "", "", cookie, false).Code != 404 {
		t.Fatal("expired key still reads private data")
	}
	s.mu.Lock()
	db = s.clone()
	k = db.AccessKeys["person"]
	k.Expires = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	db.AccessKeys["person"] = k
	err = s.commit(db)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if call("GET", "/api/state?page=alpha%2Fguide.md", "", "", cookie, false).Code != 200 {
		t.Fatal("valid scope not restored in fixture")
	}
	if err := s.revokeAccessKey("person"); err != nil {
		t.Fatal(err)
	}
	if call("GET", "/api/state?page=alpha%2Fguide.md", "", "", cookie, false).Code != 404 {
		t.Fatal("revoked session read private data")
	}
	status := call("GET", "/api/session/status", "", "", cookie, false)
	if !strings.Contains(status.Body.String(), `"authenticated":false`) {
		t.Fatal("revoked session reported active")
	}
}
