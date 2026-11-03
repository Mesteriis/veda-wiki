package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHumanTeamScopeAndSessionInvalidation(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"alpha", "beta"} {
		source(t, s, id, "private")
		if _, err := s.put(id, "guide.md", "# "+id+" private", 0); err != nil {
			t.Fatal(err)
		}
	}
	team, err := s.saveTeam("ops", []string{"alpha"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	groups := []string{"ops"}
	password := "fixture passphrase 1"
	user, err := s.saveUser("person", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil || strings.Contains(string(raw), password) {
		t.Fatal("plaintext password stored", err)
	}
	views, _ := json.Marshal(s.userViews())
	if strings.Contains(string(views), "password") || strings.Contains(string(views), "salt") {
		t.Fatal("password metadata exposed")
	}
	w := Wiki{Store: s, Token: "test-owner", Sessions: &readerSessions{}, AccessSessions: &readerSessions{}}
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
	if _, err = s.saveTeam("ops", []string{"beta"}, &team.Revision); err != nil {
		t.Fatal(err)
	}
	if read("alpha") != 404 || read("beta") != 200 {
		t.Fatal("team update did not take effect in existing session")
	}
	if _, err = s.saveTeam("ops", []string{"alpha"}, &team.Revision); err != ErrConflict {
		t.Fatal("stale team update accepted", err)
	}
	if err = migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	w.Store = s
	if read("beta") != 200 {
		t.Fatal("SQLite restart lost user or team")
	}
	disabled := true
	user, err = s.saveUser("person", nil, nil, &disabled, &user.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if read("beta") != 404 {
		t.Fatal("disabled user retained access")
	}
	disabled = false
	user, err = s.saveUser("person", nil, nil, &disabled, &user.Revision)
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
	if _, err = s.saveUser("person", nil, &changed, nil, &user.Revision); err != nil {
		t.Fatal(err)
	}
	if read("beta") != 404 {
		t.Fatal("password change retained old session")
	}
	if r := call("POST", "/api/session/login", loginBody, origin, "", nil); r.Code != 403 {
		t.Fatal("old password accepted", r.Code)
	}
}

func TestOldBinaryRejectsHumanStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_PEOPLE_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	for _, sqlite := range []bool{false, true} {
		t.Run(fmt.Sprint("sqlite=", sqlite), func(t *testing.T) {
			s := testStore(t)
			source(t, s, "alpha", "private")
			if sqlite {
				if err := migrateSQLite(s.dir); err != nil {
					t.Fatal(err)
				}
				var err error
				s, err = openStore(s.dir)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.saveTeam("ops", []string{"alpha"}, nil); err != nil {
				t.Fatal(err)
			}
			groups, password := []string{"ops"}, "synthetic test password"
			if _, err := s.saveUser("person", &groups, &password, nil, nil); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(s.dir, "state.json")
			if sqlite {
				file = filepath.Join(s.dir, sqliteFile)
			}
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
			if err == nil || ctx.Err() != nil || (!strings.Contains(string(out), "unsupported store") && !strings.Contains(string(out), "unknown SQLite record category")) {
				t.Fatal("old binary accepted human store")
			}
			after, err := os.ReadFile(file)
			if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatal("old binary changed data", err)
			}
		})
	}
}

func TestHumanAdministrationBoundaries(t *testing.T) {
	s := testStore(t)
	source(t, s, "private", "private")
	w := Wiki{Store: s, Token: "test-owner", Sessions: &readerSessions{}, AccessSessions: &readerSessions{}}
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
	if _, err := s.saveTeam("ops", []string{"alpha"}, nil); err != nil {
		t.Fatal(err)
	}
	teams := []string{"ops"}
	password := "synthetic old password"
	if _, err := s.saveUser("person", &teams, &password, nil, nil); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "test-owner", Sessions: &readerSessions{}}
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
	if _, err := s.saveTeam("ops", []string{"alpha"}, nil); err != nil {
		t.Fatal(err)
	}
	teams := []string{"ops"}
	password := "синтетический пароль длинный"
	if _, err := s.saveUser("person", &teams, &password, nil, nil); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Sessions: &readerSessions{}}
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

func TestResumePeopleRestoreFixture(t *testing.T) {
	dir := os.Getenv("VEDA_PEOPLE_RESTORED_DIR")
	if dir == "" {
		t.Skip("restored fixture not provided")
	}
	s, err := openStore(dir)
	if err != nil || s.db.Version != 15 {
		t.Fatal("invalid restored user store", err)
	}
	version, ok := s.authenticateUser("person", "synthetic fixture password")
	if !ok {
		t.Fatal("restored user login failed")
	}
	s.mu.RLock()
	scope := s.userScopeLocked("person", version)
	s.mu.RUnlock()
	if len(scope) != 1 || scope[0] != "from" {
		t.Fatal("restored team scope incorrect")
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Synthetic target"})
	f.Modify(func() { f.Private = true })
	if err = s.pauseConnection("handbook", false); err != nil {
		t.Fatal(err)
	}
	manager := newSyncManager(s, f.Client())
	manager.Once(context.Background())
	manager.Once(context.Background())
	if len(s.jobs()) != 1 || s.jobs()[0].State != "complete" || len(s.moves()) != 1 || s.moves()[0].State != "complete" || !s.db.Articles["from/a.md"].Deleted {
		t.Fatal("restored queue did not complete")
	}
	ref := s.db.Sources["handbook"].Assets["img/icon.png"]
	if ref.SHA == "" || digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA || f.Contents()["related.md"] != "# Related fixture" {
		t.Fatal("restored bundle incomplete")
	}
}

// The browser access-management path cannot remove the last active
// administrator. Owner provisioning (setUserAdmin) remains the recovery path.
func TestLastActiveAdministratorCannotBeDisabled(t *testing.T) {
	s := testStore(t)
	password := "синтетический пароль администратора"
	if err := s.createSuperuser("first-admin", password); err != nil {
		t.Fatal(err)
	}
	disabled := true
	if _, err := s.saveUser("first-admin", nil, nil, &disabled, intPointer(1)); err != ErrConflict {
		t.Fatal("last active administrator disabled", err)
	}
	if err := s.createSuperuser("second-admin", password); err != nil {
		t.Fatal(err)
	}
	if _, err := s.saveUser("first-admin", nil, nil, &disabled, intPointer(1)); err != nil {
		t.Fatal("administrator with an active peer could not be disabled", err)
	}
	if _, err := s.saveUser("second-admin", nil, nil, &disabled, intPointer(1)); err != ErrConflict {
		t.Fatal("remaining administrator disabled", err)
	}
}

func intPointer(v int) *int { return &v }
