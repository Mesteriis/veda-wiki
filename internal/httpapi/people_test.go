package httpapi

import (
	"encoding/json"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHumanTeamScopeAndSessionInvalidation(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		source(t, s, id, "private")
		if _, err := s.put(id, "guide.md", "# "+id+" private", 0); err != nil {
			t.Fatal(err)
		}
	}
	team, err := s.auth().SaveTeam("ops", []string{"alpha"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	groups := []string{"ops"}
	password := "fixture passphrase 1"
	user, err := s.auth().SaveUser("person", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), "state.json"))
	if err != nil || strings.Contains(string(raw), password) {
		t.Fatal("plaintext password stored", err)
	}
	views, _ := json.Marshal(s.auth().UserViews())
	if strings.Contains(string(views), "password") || strings.Contains(string(views), "salt") {
		t.Fatal("password metadata exposed")
	}
	w := Wiki{Store: s, Token: "test-owner", Sessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}}
	call := func(method, path, body, origin, bearer string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	origin := "http://localhost:8080"
	loginBody := fmt.Sprintf(`{"username":"person","password":%q}`, password)
	if r := call("POST", "/api/session/login", loginBody, "http://evil.example", "", nil); r.Code != 403 {
		t.Fatal("cross-origin login", r.Code)
	}
	login := call("POST", "/api/session/login", loginBody, origin, "", nil)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatal("user login failed", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	read := func(id string) int {
		return call("GET", "/api/v1/articles?source_id="+id+"&path=guide.md", "", "", "", cookie).Code
	}
	if read("alpha") != 200 || read("beta") != 404 {
		t.Fatal("initial team scope incorrect")
	}
	if r := call("GET", "/api/v1/users", "", "", "", cookie); r.Code != 404 {
		t.Fatal("user listed accounts", r.Code)
	}
	if r := call("POST", "/api/v1/articles", `{}`, origin, "", cookie); r.Code != 403 {
		t.Fatal("user wrote article", r.Code)
	}
	if _, err = s.auth().SaveTeam("ops", []string{"beta"}, &team.Revision); err != nil {
		t.Fatal(err)
	}
	if read("alpha") != 404 || read("beta") != 200 {
		t.Fatal("team update did not take effect in existing session")
	}
	if _, err = s.auth().SaveTeam("ops", []string{"alpha"}, &team.Revision); err != domain.ErrConflict {
		t.Fatal("stale team update accepted", err)
	}
	if err = store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	w.Store = s
	if read("beta") != 200 {
		t.Fatal("SQLite restart lost user or team")
	}
	disabled := true
	user, err = s.auth().SaveUser("person", nil, nil, &disabled, &user.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if read("beta") != 404 {
		t.Fatal("disabled user retained access")
	}
	disabled = false
	user, err = s.auth().SaveUser("person", nil, nil, &disabled, &user.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if read("beta") != 404 {
		t.Fatal("reenable revived old session")
	}
	login = call("POST", "/api/session/login", loginBody, origin, "", nil)
	if login.Code != 200 {
		t.Fatal("reenabled user login failed", login.Code)
	}
	cookie = login.Result().Cookies()[0]
	changed := "different fixture passphrase"
	if _, err = s.auth().SaveUser("person", nil, &changed, nil, &user.Revision); err != nil {
		t.Fatal(err)
	}
	if read("beta") != 404 {
		t.Fatal("password change retained old session")
	}
	if r := call("POST", "/api/session/login", loginBody, origin, "", nil); r.Code != 403 {
		t.Fatal("old password accepted", r.Code)
	}
}

func TestHumanAdministrationBoundaries(t *testing.T) {
	s := testStore(t)
	source(t, s, "private", "private")
	w := Wiki{Store: s, Token: "test-owner", Sessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}}
	create := `{"id":"ops","sources":["private"]}`
	if r := request(w, "POST", "/api/v1/teams", create, false); r.Code != 403 {
		t.Fatal("anonymous team write", r.Code)
	}
	if r := request(w, "POST", "/api/v1/teams", create, true); r.Code != 201 {
		t.Fatal("owner team create", r.Code)
	}
	if r := request(w, "POST", "/api/v1/users", `{"id":"person","teams":["ops"],"password":"short"}`, true); r.Code != 400 {
		t.Fatal("weak password accepted", r.Code)
	}
	if r := request(w, "POST", "/api/v1/users", `{"id":"person","teams":["ops"],"password":"synthetic phrase"}`, true); r.Code != 201 || strings.Contains(r.Body.String(), "synthetic phrase") || strings.Contains(r.Body.String(), "hash") {
		t.Fatal("user create failed or secret leaked", r.Code)
	}
	if r := request(w, "GET", "/api/v1/users", "", false); r.Code != 404 {
		t.Fatal("anonymous user listing", r.Code)
	}
}

func TestHumanPasswordChangeRevokesAllSessions(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	if _, err := s.auth().SaveTeam("ops", []string{"alpha"}, nil); err != nil {
		t.Fatal(err)
	}
	teams := []string{"ops"}
	password := "synthetic old password"
	if _, err := s.auth().SaveUser("person", &teams, &password, nil, nil); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "test-owner", Sessions: &auth.Sessions{}}
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
	origin := "http://localhost:8080"
	login := fmt.Sprintf(`{"username":"person","password":%q}`, password)
	first := call("POST", "/api/session/login", login, origin, nil, false)
	second := call("POST", "/api/session/login", login, origin, nil, false)
	if first.Code != 200 || second.Code != 200 {
		t.Fatal("login failed")
	}
	a, b := first.Result().Cookies()[0], second.Result().Cookies()[0]
	if r := call("GET", "/api/session/profile", "", "", a, false); r.Code != 200 || !strings.Contains(r.Body.String(), `"password_change":true`) {
		t.Fatal("password profile missing")
	}
	body := fmt.Sprintf(`{"current_password":%q,"new_password":"synthetic new password"}`, password)
	if r := call("POST", "/api/session/password", body, "http://evil.example", a, false); r.Code != 403 {
		t.Fatal("cross-origin password change", r.Code)
	}
	if r := call("POST", "/api/session/password", `{"current_password":"incorrect password","new_password":"synthetic new password"}`, origin, a, false); r.Code != 403 {
		t.Fatal("wrong current password accepted", r.Code)
	}
	if r := call("POST", "/api/session/password", body, origin, a, false); r.Code != 200 {
		t.Fatal("password change failed", r.Code)
	}
	for _, cookie := range []*http.Cookie{a, b} {
		if r := call("GET", "/api/session/status", "", "", cookie, false); !strings.Contains(r.Body.String(), `"authenticated":false`) {
			t.Fatal("old session survived")
		}
	}
	if r := call("POST", "/api/session/login", login, origin, nil, false); r.Code != 403 {
		t.Fatal("old password accepted", r.Code)
	}
	if r := call("POST", "/api/session/login", `{"username":"person","password":"synthetic new password"}`, origin, nil, false); r.Code != 200 {
		t.Fatal("new password rejected", r.Code)
	}
	if r := call("GET", "/api/session/profile", "", "", a, true); r.Code != 404 {
		t.Fatal("public profile available", r.Code)
	}
}

func TestHumanLoginUsesGenericFailuresAndRateLimit(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	if _, err := s.auth().SaveTeam("ops", []string{"alpha"}, nil); err != nil {
		t.Fatal(err)
	}
	teams := []string{"ops"}
	password := "синтетический пароль длинный"
	if _, err := s.auth().SaveUser("person", &teams, &password, nil, nil); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Sessions: &auth.Sessions{}}
	login := func(id, secret string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(map[string]string{"username": id, "password": secret})
		r := httptest.NewRequest("POST", "http://localhost:8080/api/session/login", strings.NewReader(string(data)))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://localhost:8080")
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	a := login("person", "wrong synthetic password")
	b := login("missing", "wrong synthetic password")
	if a.Code != 403 || b.Code != 403 || a.Body.String() != b.Body.String() {
		t.Fatal("login reveals account existence")
	}
	// Unknown and existing identities are limited alike, per identity.
	for i := 0; i < 9; i++ {
		if r := login("missing", "wrong synthetic password"); r.Code != 403 {
			t.Fatal("unexpected rate state", r.Code)
		}
	}
	if r := login("missing", "wrong synthetic password"); r.Code != 429 {
		t.Fatal("unknown identity rate limit missing", r.Code)
	}
	if r := login("person", password); r.Code != 200 {
		t.Fatal("another identity's failures locked out this account", r.Code)
	}
	// Every check counts, including the successful login above.
	for i := 0; i < 8; i++ {
		if r := login("person", "wrong synthetic password"); r.Code != 403 {
			t.Fatal("unexpected rate state", r.Code)
		}
	}
	if r := login("person", password); r.Code != 429 {
		t.Fatal("login rate limit missing", r.Code)
	}
}
