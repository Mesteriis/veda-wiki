package main

import (
	"crypto/sha256"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReaderSessionBoundaries(t *testing.T) {
	store := testStore(t)
	source(t, store, "private", "private")
	if _, e := store.put("private", "guide.md", "# private fixture", 0); e != nil {
		t.Fatal(e)
	}
	sessions := &readerSessions{}
	w := Wiki{Store: store, Token: "owner", ReaderToken: "read-only", Sessions: sessions, AllowedHosts: []string{"localhost"}}
	request := func(method, path, body, origin string, cookie *http.Cookie, public bool, remote, host string, tlsOn bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		r.Host = host
		r.RemoteAddr = remote
		if tlsOn {
			r.TLS = &tls.ConnectionState{}
		}
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
	login := func(origin string) *httptest.ResponseRecorder {
		return request("POST", "/api/session/login", `{"key":"read-only"}`, origin, nil, false, "127.0.0.1:1234", "localhost:8080", false)
	}
	if r := login("http://evil.example"); r.Code != 403 {
		t.Fatal("cross origin login", r.Code)
	}
	if r := request("POST", "/api/session/login", `{"key":"read-only"}`, "http://localhost:8080", nil, false, "192.0.2.1:1234", "localhost:8080", false); r.Code != 404 {
		t.Fatal("insecure remote login", r.Code)
	}
	if r := request("POST", "/api/session/login", `{"key":"read-only"}`, "http://localhost:8080", nil, true, "127.0.0.1:1234", "localhost:8080", false); r.Code != 404 {
		t.Fatal("public login", r.Code)
	}
	r := login("http://localhost:8080")
	if r.Code != 200 || len(r.Result().Cookies()) != 1 {
		t.Fatal("login failed", r.Code)
	}
	cookie := r.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure cookie attributes")
	}
	read := func(public bool) *httptest.ResponseRecorder {
		return request("GET", "/api/state?page=private%2Fguide.md", "", "", cookie, public, "127.0.0.1:1234", "localhost:8080", false)
	}
	if r = read(false); r.Code != 200 || !strings.Contains(r.Body.String(), "private fixture") {
		t.Fatal("session cannot read", r.Code)
	}
	if r = read(true); r.Code != 404 {
		t.Fatal("public accepts private session", r.Code)
	}
	if r = request("POST", "/api/v1/articles", `{}`, "http://localhost:8080", cookie, false, "127.0.0.1:1234", "localhost:8080", false); r.Code != 403 {
		t.Fatal("session wrote", r.Code)
	}
	if r = request("GET", "/api/v1/conflicts", "", "", cookie, false, "127.0.0.1:1234", "localhost:8080", false); r.Code != 404 {
		t.Fatal("session admin", r.Code)
	}
	if r = request("POST", "/api/session/logout", "", "http://evil.example", cookie, false, "127.0.0.1:1234", "localhost:8080", false); r.Code != 403 {
		t.Fatal("cross origin logout", r.Code)
	}
	if r = request("POST", "/api/session/logout", "", "http://localhost:8080", cookie, false, "127.0.0.1:1234", "localhost:8080", false); r.Code != 200 || read(false).Code != 404 {
		t.Fatal("logout did not revoke")
	}
	r = request("POST", "/api/session/login", `{"key":"read-only"}`, "https://localhost:8080", nil, false, "192.0.2.1:1234", "localhost:8080", true)
	if r.Code != 200 || !r.Result().Cookies()[0].Secure {
		t.Fatal("TLS session cookie", r.Code)
	}
}
func TestReaderSessionExpiryAndRateLimit(t *testing.T) {
	token := strings.Repeat("a", 64)
	expired := sha256.Sum256([]byte(token))
	expirySessions := &readerSessions{active: map[[32]byte]time.Time{expired: time.Now().Add(-time.Second)}}
	req := httptest.NewRequest("GET", "http://localhost/", nil)
	req.Host = "localhost"
	req.RemoteAddr = "127.0.0.1:1"
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	if expirySessions.valid(req) {
		t.Fatal("expired session accepted")
	}
	s := &readerSessions{active: map[[32]byte]time.Time{}}
	w := Wiki{ReaderToken: "key", Sessions: s}
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "http://localhost/api/session/login", strings.NewReader(`{"key":"bad"}`))
		r.Host = "localhost"
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Origin", "http://localhost")
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		if i == 10 && out.Code != 429 {
			t.Fatal("rate limit", out.Code)
		}
	}
}
