package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdministratorSessionRequiresRoleAndIsRevoked(t *testing.T) {
	s := testStore(t)
	groups := []string{}
	password := "synthetic password"
	user, err := s.auth().SaveUser("admin", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", Sync: newSyncManager(s, githubtest.New(t, map[string]string{}).Client()), AdminSessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}}
	call := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		if method == "POST" {
			r.Header.Set("Origin", "http://localhost:8080")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	body := `{"username":"admin","password":"synthetic password"}`
	if r := call("POST", "/api/admin/session/login", body, nil); r.Code != 403 {
		t.Fatal("reader gained admin", r.Code)
	}
	if err := s.auth().SetUserAdmin("admin", true, user.Revision); err != nil {
		t.Fatal(err)
	}
	login := call("POST", "/api/admin/session/login", body, nil)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatal("admin login failed", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	accessLogin := call("POST", "/api/access/session/login", body, nil)
	if accessLogin.Code != 200 || len(accessLogin.Result().Cookies()) != 1 {
		t.Fatal("access login failed", accessLogin.Code)
	}
	accessCookie := accessLogin.Result().Cookies()[0]
	if r := call("GET", "/api/access/users", "", accessCookie); r.Code != 200 {
		t.Fatal("admin cannot manage users", r.Code)
	}
	if r := call("GET", "/api/admin/github/accounts", "", cookie); r.Code != 200 {
		t.Fatal("admin cannot access github", r.Code)
	}
	if r := call("POST", "/api/v1/articles", `{}`, cookie); r.Code != 403 {
		t.Fatal("admin session gained API write", r.Code)
	}
	if err := s.auth().SetUserAdmin("admin", false, user.Revision+1); err != nil {
		t.Fatal(err)
	}
	if r := call("GET", "/api/admin/github/accounts", "", cookie); r.Code != 404 {
		t.Fatal("revoked admin retained access", r.Code)
	}
	if r := call("GET", "/api/access/users", "", accessCookie); r.Code != 404 {
		t.Fatal("revoked admin retained access management", r.Code)
	}
}

func TestAdministratorBootstrapIsOneTimeAndLocalRootOnly(t *testing.T) {
	s := testStore(t)
	w := Wiki{Store: s, ReaderLocal: true, LocalImportSource: "infra", AdminSessions: &auth.Sessions{}}
	provision := func(root bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/local/bootstrap-admin", strings.NewReader(`{"id":"operator"}`))
		if root {
			r = r.WithContext(context.WithValue(r.Context(), LocalRootKey{}, true))
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	if provision(false, "").Code == 201 || provision(true, "http://localhost").Code == 201 {
		t.Fatal("untrusted provisioning succeeded")
	}
	out := provision(true, "")
	if out.Code != 201 {
		t.Fatal("provisioning failed", out.Code)
	}
	var response struct {
		Token string `json:"setup_token"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil || len(response.Token) != 64 {
		t.Fatal("invalid setup token")
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), "state.json"))
	if err != nil || strings.Contains(string(raw), response.Token) {
		t.Fatal("setup token persisted in plaintext", err)
	}
	if _, ok := s.auth().AuthenticateUser("operator", response.Token); ok {
		t.Fatal("setup token accepted as password")
	}
	oldToken := response.Token
	rotated := provision(true, "")
	if rotated.Code != 201 || json.Unmarshal(rotated.Body.Bytes(), &response) != nil || response.Token == oldToken {
		t.Fatal("pending setup could not be reissued")
	}
	if err := s.auth().FinishAdminSetup("operator", oldToken, "synthetic long password"); err == nil {
		t.Fatal("old setup token accepted")
	}
	if err := s.auth().FinishAdminSetup("operator", "wrong synthetic token", "synthetic long password"); err == nil {
		t.Fatal("wrong token accepted")
	}
	if err := s.auth().FinishAdminSetup("operator", response.Token, "synthetic long password"); err != nil {
		t.Fatal(err)
	}
	if err := s.auth().FinishAdminSetup("operator", response.Token, "different long password"); err == nil {
		t.Fatal("reused token accepted")
	}
	if provision(true, "").Code == 201 {
		t.Fatal("configured administrator replaced")
	}
	version, ok := s.auth().AuthenticateUser("operator", "synthetic long password")
	if !ok || !s.auth().AdminSessionValid("operator", version) {
		t.Fatal("created administrator cannot login")
	}
}

func TestAdminSetupRequiresSecureSameOriginAndExpires(t *testing.T) {
	s := testStore(t)
	token, err := s.auth().BootstrapAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, AdminSessions: &auth.Sessions{}}
	body, _ := json.Marshal(map[string]string{"username": "operator", "setup_token": token, "password": "synthetic password"})
	call := func(origin, remote string, public bool) int {
		r := httptest.NewRequest("POST", "http://localhost:8080/api/admin/session/setup", bytes.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Origin", origin)
		out := httptest.NewRecorder()
		target := w
		target.Public = public
		target.ServeHTTP(out, r)
		return out.Code
	}
	if call("http://evil.example", "127.0.0.1:1", false) != 403 || call("http://localhost:8080", "192.0.2.1:1", false) != 404 || call("http://localhost:8080", "127.0.0.1:1", true) != 404 {
		t.Fatal("untrusted setup request accepted")
	}
	if call("http://localhost:8080", "127.0.0.1:1", false) != 200 {
		t.Fatal("valid setup failed")
	}
	if call("http://localhost:8080", "127.0.0.1:1", false) == 200 {
		t.Fatal("setup replay accepted")
	}
}

func TestNamedAdministratorReaderSessionGrantsAdminWithoutSecondLogin(t *testing.T) {
	s := testStore(t)
	groups := []string{}
	password := "synthetic password"
	admin, err := s.auth().SaveUser("administrator", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.auth().SetUserAdmin("administrator", true, admin.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.auth().SaveUser("reader", &groups, &password, nil, nil); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", ReaderToken: "reader-key", Sessions: &auth.Sessions{}, AdminSessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}, Sync: newSyncManager(s, githubtest.New(t, map[string]string{}).Client())}
	call := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	origin := "http://localhost:8080"
	login := func(body string) *http.Cookie {
		r := call("POST", "/api/session/login", body, origin, nil)
		if r.Code != 200 || len(r.Result().Cookies()) != 1 {
			t.Fatal("login failed", r.Code)
		}
		return r.Result().Cookies()[0]
	}
	reader := login(`{"username":"reader","password":"synthetic password"}`)
	if r := call("GET", "/api/admin/github/accounts", "", "", reader); r.Code != 404 {
		t.Fatal("ordinary reader gained admin", r.Code)
	}
	key := login(`{"key":"reader-key"}`)
	if r := call("GET", "/api/admin/github/accounts", "", "", key); r.Code != 404 {
		t.Fatal("reader key gained admin", r.Code)
	}
	cookie := login(`{"username":"administrator","password":"synthetic password"}`)
	for _, path := range []string{"/api/admin/github/accounts", "/api/access/users"} {
		if r := call("GET", path, "", "", cookie); r.Code != 200 {
			t.Fatal("administrator denied", path, r.Code)
		}
	}
	status := call("GET", "/api/admin/session/status", "", "", cookie)
	if !strings.Contains(status.Body.String(), `"via_reader_session":true`) {
		t.Fatal("shared session not reported")
	}
	if r := call("POST", "/api/admin/github/accounts", `{}`, "http://evil.example", cookie); r.Code != 403 {
		t.Fatal("cross-origin admin request accepted", r.Code)
	}
	if r := call("POST", "/api/v1/articles", `{}`, origin, cookie); r.Code != 403 {
		t.Fatal("reader session obtained write API", r.Code)
	}
	if r := call("POST", "/api/session/logout", `{}`, origin, cookie); r.Code != 200 {
		t.Fatal("logout failed", r.Code)
	}
	if r := call("GET", "/api/admin/github/accounts", "", "", cookie); r.Code != 404 {
		t.Fatal("logout did not revoke admin", r.Code)
	}
}

func TestReaderLogoutRevokesIndependentAdminCookies(t *testing.T) {
	s := testStore(t)
	groups := []string{}
	password := "synthetic password"
	u, err := s.auth().SaveUser("admin", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.auth().SetUserAdmin("admin", true, u.Revision); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", Sessions: &auth.Sessions{}, AdminSessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}, Sync: newSyncManager(s, githubtest.New(t, map[string]string{}).Client())}
	call := func(method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1"
		if method == "POST" {
			r.Header.Set("Origin", "http://localhost:8080")
		}
		for _, c := range cookies {
			r.AddCookie(c)
		}
		o := httptest.NewRecorder()
		w.ServeHTTP(o, r)
		return o
	}
	login := func(path, body string) *http.Cookie {
		r := call("POST", path, body)
		if r.Code != 200 {
			t.Fatal("login failed", r.Code)
		}
		return r.Result().Cookies()[0]
	}
	a := login("/api/admin/session/login", `{"key":"owner"}`)
	x := login("/api/access/session/login", `{"key":"owner"}`)
	r := login("/api/session/login", `{"username":"admin","password":"synthetic password"}`)
	if out := call("POST", "/api/session/logout", `{}`, r, a, x); out.Code != 200 || len(out.Result().Cookies()) != 3 {
		t.Fatal("logout did not clear all cookies")
	}
	if out := call("GET", "/api/admin/github/accounts", "", a); out.Code != 404 {
		t.Fatal("admin cookie survived logout", out.Code)
	}
	if out := call("GET", "/api/access/users", "", x); out.Code != 404 {
		t.Fatal("access cookie survived logout", out.Code)
	}
}
