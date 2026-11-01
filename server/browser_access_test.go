package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrowserAccessAdministrationBoundaries(t *testing.T) {
	s := testStore(t)
	source(t, s, "allowed", "private")
	source(t, s, "other", "private")
	w := Wiki{Store: s, Token: "owner", ReaderToken: "reader", Sessions: &readerSessions{}, AdminSessions: &readerSessions{}, AccessSessions: &readerSessions{}}
	call := func(method, path, body, origin string, cookie *http.Cookie, public bool, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.RemoteAddr = remote
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
	local := "127.0.0.1:1000"
	origin := "http://localhost:8080"
	if r := call("POST", "/api/access/session/login", `{"key":"owner"}`, "http://evil.example", nil, false, local); r.Code != 403 {
		t.Fatal("cross-origin login", r.Code)
	}
	if r := call("POST", "/api/access/session/login", `{"key":"owner"}`, origin, nil, false, "192.0.2.1:1000"); r.Code != 404 {
		t.Fatal("remote HTTP login", r.Code)
	}
	if r := call("POST", "/api/access/session/login", `{"key":"owner"}`, origin, nil, true, local); r.Code != 404 {
		t.Fatal("public login", r.Code)
	}
	login := call("POST", "/api/access/session/login", `{"key":"owner"}`, origin, nil, false, local)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatal("login failed", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	if cookie.Name != accessSessionCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("invalid cookie")
	}
	githubLogin := call("POST", "/api/admin/session/login", `{"key":"owner"}`, origin, nil, false, local)
	if githubLogin.Code != 200 || len(githubLogin.Result().Cookies()) != 1 || call("GET", "/api/access/keys", "", "", githubLogin.Result().Cookies()[0], false, local).Code != 404 {
		t.Fatal("GitHub session granted access administration")
	}
	if r := call("GET", "/api/access/sources", "", "", cookie, false, local); r.Code != 200 {
		t.Fatal("sources unavailable", r.Code)
	}
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	body := `{"id":"test-agent","sources":["allowed"],"expires_at":"` + expires + `"}`
	if r := call("POST", "/api/access/keys", body, "http://evil.example", cookie, false, local); r.Code != 403 {
		t.Fatal("cross-origin creation", r.Code)
	}
	created := call("POST", "/api/access/keys", body, origin, cookie, false, local)
	var result struct {
		Key string `json:"key"`
	}
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &result) != nil || len(result.Key) != 64 {
		t.Fatal("key creation failed", created.Code)
	}
	if got := s.accessScope(result.Key, time.Now()); len(got) != 1 || got[0] != "allowed" {
		t.Fatal("incorrect key scope")
	}
	listed := call("GET", "/api/access/keys", "", "", cookie, false, local)
	if listed.Code != 200 || strings.Contains(listed.Body.String(), result.Key) {
		t.Fatal("key leaked in list")
	}
	if r := call("GET", "/api/v1/access-keys", "", "", cookie, false, local); r.Code != 404 {
		t.Fatal("owner API accessible", r.Code)
	}
	if r := call("GET", "/api/admin/github/accounts", "", "", cookie, false, local); r.Code != 404 {
		t.Fatal("GitHub API accessible", r.Code)
	}
	if r := call("GET", "/api/v1/sources", "", "", cookie, false, local); r.Code != 200 || strings.Contains(r.Body.String(), "allowed") {
		t.Fatal("access session granted private reader rights", r.Code)
	}
	if r := call("POST", "/api/v1/articles", `{}`, origin, cookie, false, local); r.Code != 403 {
		t.Fatal("article write accessible", r.Code)
	}
	if r := call("POST", "/api/access/keys/revoke", `{"id":"test-agent"}`, origin, cookie, true, local); r.Code != 404 {
		t.Fatal("public administration", r.Code)
	}
	if r := call("POST", "/api/access/keys/revoke", `{"id":"test-agent"}`, origin, cookie, false, local); r.Code != 200 || s.accessScope(result.Key, time.Now()) != nil {
		t.Fatal("revoke failed", r.Code)
	}
	if r := call("POST", "/api/access/session/logout", "", origin, cookie, false, local); r.Code != 200 {
		t.Fatal("logout failed", r.Code)
	}
	if r := call("GET", "/api/access/keys", "", "", cookie, false, local); r.Code != 404 {
		t.Fatal("logout ineffective", r.Code)
	}
}
