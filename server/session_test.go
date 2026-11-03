package main

import (
	"crypto/sha256"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestAdminGitHubSessionBoundaries(t *testing.T) {
	store := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	w := Wiki{Store: store, Token: "owner", ReaderToken: "reader", Sessions: &readerSessions{}, AdminSessions: &readerSessions{}, Sync: newSyncManager(store, f.client())}
	request := func(method, path, body, origin string, cookie *http.Cookie, public bool, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.Host, r.RemoteAddr = "localhost:8080", remote
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		target := w
		target.Public = public
		out := httptest.NewRecorder()
		target.ServeHTTP(out, r)
		return out
	}
	login := func(key, origin string, public bool, remote string) *httptest.ResponseRecorder {
		return request("POST", "/api/admin/session/login", `{"key":"`+key+`"}`, origin, nil, public, remote)
	}
	if login("reader", "http://localhost:8080", false, "127.0.0.1:1").Code != 403 {
		t.Fatal("reader key obtained admin session")
	}
	if login("owner", "http://evil.example", false, "127.0.0.1:1").Code != 403 {
		t.Fatal("cross-origin login accepted")
	}
	if login("owner", "http://localhost:8080", true, "127.0.0.1:1").Code != 404 {
		t.Fatal("public admin login accepted")
	}
	if login("owner", "http://localhost:8080", false, "192.0.2.1:1").Code != 404 {
		t.Fatal("remote plaintext login accepted")
	}
	r := login("owner", "http://localhost:8080", false, "127.0.0.1:1")
	if r.Code != 200 || len(r.Result().Cookies()) != 1 {
		t.Fatal("admin login failed", r.Code)
	}
	cookie := r.Result().Cookies()[0]
	if cookie.Name != adminSessionCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure cookie")
	}
	readerLogin := request("POST", "/api/session/login", `{"key":"reader"}`, "http://localhost:8080", nil, false, "127.0.0.1:1")
	if readerLogin.Code != 200 {
		t.Fatal("reader login")
	}
	readerCookie := readerLogin.Result().Cookies()[0]
	if request("GET", "/api/admin/github/accounts", "", "", readerCookie, false, "127.0.0.1:1").Code != 404 {
		t.Fatal("reader cookie obtained admin access")
	}
	if request("GET", "/api/admin/github/accounts", "", "", cookie, true, "127.0.0.1:1").Code != 404 {
		t.Fatal("public admin accepted")
	}
	if request("POST", "/api/admin/github/accounts", `{}`, "http://evil.example", cookie, false, "127.0.0.1:1").Code != 403 {
		t.Fatal("cross-origin admin accepted")
	}
	if request("POST", "/api/v1/articles", `{}`, "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 403 {
		t.Fatal("admin session wrote article")
	}
	if request("GET", "/api/v1/github/accounts", "", "", cookie, false, "127.0.0.1:1").Code != 404 {
		t.Fatal("admin cookie authorized owner API")
	}
	if request("GET", "/api/admin/github/outbox", "", "", cookie, false, "127.0.0.1:1").Code != 404 {
		t.Fatal("admin endpoint escaped allowlist")
	}
	body := `{"id":"fixture","kind":"token","token":"` + fixtureAccess + `"}`
	if r = request("POST", "/api/admin/github/accounts", body, "http://localhost:8080", cookie, false, "127.0.0.1:1"); r.Code != 201 {
		t.Fatal("account registration", r.Code)
	}
	if r = request("GET", "/api/admin/github/accounts", "", "", cookie, false, "127.0.0.1:1"); r.Code != 200 || strings.Contains(r.Body.String(), fixtureAccess) {
		t.Fatal("account listing exposed a credential")
	}
	if r = request("POST", "/api/admin/github/connections", `{"id":"wiki","repository":"org/handbook","account_id":"fixture","write":true}`, "http://localhost:8080", cookie, false, "127.0.0.1:1"); r.Code != 202 {
		t.Fatal("connection", r.Code)
	}
	config := `{"id":"wiki","account_id":"fixture","write":false,"expected_config_revision":1}`
	if request("PUT", "/api/admin/github/connections", config, "http://evil.example", cookie, false, "127.0.0.1:1").Code != 403 || request("PUT", "/api/admin/github/connections", config, "http://localhost:8080", readerCookie, false, "127.0.0.1:1").Code != 404 {
		t.Fatal("unauthorized configuration accepted")
	}
	if request("PUT", "/api/admin/github/connections", config, "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 200 {
		t.Fatal("configuration failed")
	}
	if request("PUT", "/api/admin/github/connections", config, "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 409 {
		t.Fatal("stale configuration accepted")
	}
	if request("POST", "/api/admin/github/sync", `{"id":"wiki"}`, "http://localhost:8080", readerCookie, false, "127.0.0.1:1").Code != 404 {
		t.Fatal("reader cookie scheduled sync")
	}
	if request("POST", "/api/admin/github/sync", `{"id":"wiki"}`, "http://evil.example", cookie, false, "127.0.0.1:1").Code != 403 {
		t.Fatal("cross-origin sync accepted")
	}
	if request("POST", "/api/admin/github/sync", `{"id":"wiki"}`, "http://localhost:8080", cookie, true, "127.0.0.1:1").Code != 404 {
		t.Fatal("public admin sync accepted")
	}
	if r = request("POST", "/api/admin/github/sync", `{"id":"wiki"}`, "http://localhost:8080", cookie, false, "127.0.0.1:1"); r.Code != 202 {
		t.Fatal("admin sync not scheduled", r.Code)
	}
	if request("POST", "/api/admin/github/sync", `{"id":"missing"}`, "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 404 {
		t.Fatal("unknown connection scheduled")
	}
	if request("POST", "/api/admin/github/pause", `{"id":"wiki","paused":true}`, "http://evil.example", cookie, false, "127.0.0.1:1").Code != 403 || request("POST", "/api/admin/github/pause", `{"id":"wiki","paused":true}`, "http://localhost:8080", readerCookie, false, "127.0.0.1:1").Code != 404 {
		t.Fatal("unauthorized pause accepted")
	}
	if request("POST", "/api/admin/github/pause", `{"id":"wiki"}`, "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 400 {
		t.Fatal("missing pause state accepted")
	}
	if request("POST", "/api/admin/github/pause", `{"id":"wiki","paused":true}`, "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 202 {
		t.Fatal("pause failed")
	}
	if request("PUT", "/api/admin/github/connections", `{"id":"wiki","write":false,"expected_config_revision":3}`, "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 200 || store.connections()[0].State != "paused" {
		t.Fatal("configuration lost paused state")
	}
	if request("POST", "/api/admin/github/sync", `{"id":"wiki"}`, "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 409 {
		t.Fatal("paused connection scheduled")
	}
	if request("POST", "/api/admin/github/pause", `{"id":"wiki","paused":false}`, "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 202 {
		t.Fatal("resume failed")
	}
	if request("POST", "/api/admin/session/logout", "", "http://localhost:8080", cookie, false, "127.0.0.1:1").Code != 200 {
		t.Fatal("logout")
	}
	if request("GET", "/api/admin/github/accounts", "", "", cookie, false, "127.0.0.1:1").Code != 404 {
		t.Fatal("logout did not revoke")
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
	key := strings.Repeat("b", 64)
	w := Wiki{ReaderToken: key, Sessions: s}
	login := func(body string) int {
		r := httptest.NewRequest("POST", "http://localhost/api/session/login", strings.NewReader(body))
		r.Host = "localhost"
		r.RemoteAddr = "127.0.0.1:1"
		r.Header.Set("Origin", "http://localhost")
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out.Code
	}
	// Random 256-bit keys cannot be guessed, so failed key attempts must not
	// let an anonymous client lock out key and link logins.
	for i := 0; i < 11; i++ {
		if code := login(`{"key":"bad"}`); code != 403 {
			t.Fatal("bad key", i, code)
		}
	}
	for i := 0; i < 11; i++ {
		if code := login(`{"username":"someone","password":"wrong synthetic password"}`); i == 10 && code != 429 {
			t.Fatal("password rate limit", code)
		}
	}
	if code := login(`{"key":"` + key + `"}`); code != 200 {
		t.Fatal("failed attempts locked out a valid key", code)
	}
}

func TestLoginAttemptTableIsBounded(t *testing.T) {
	s := &readerSessions{}
	now := time.Now()
	for i := 0; i < attemptIdentityMax; i++ {
		if !s.allowAttempt("user", strconv.Itoa(i), now) {
			t.Fatal("identity rejected before the table was full", i)
		}
	}
	if s.allowAttempt("user", "new", now) {
		t.Fatal("full table accepted a new identity")
	}
	if !s.allowAttempt("user", "0", now) {
		t.Fatal("full table locked out a known identity below its limit")
	}
	if !s.allowAttempt("user", "new", now.Add(2*time.Minute)) || len(s.attempts) != 1 {
		t.Fatal("expired identities were not pruned", len(s.attempts))
	}
}
