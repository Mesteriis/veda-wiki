package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdministratorRoleInvalidatesOldSessionsAndSurvivesReopen(t *testing.T) {
	s := testStore(t)
	groups := []string{}
	password := "synthetic password"
	user, err := s.saveUser("admin", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldVersion, ok := s.authenticateUser("admin", password)
	if !ok || s.adminSessionValid("admin", oldVersion) {
		t.Fatal("reader is administrator")
	}
	if err = s.setUserAdmin("admin", true, user.Revision); err != nil {
		t.Fatal(err)
	}
	if s.adminSessionValid("admin", oldVersion) || s.userSessionValid("admin", oldVersion) {
		t.Fatal("old session survived role change")
	}
	version, ok := s.authenticateUser("admin", password)
	if !ok || !s.adminSessionValid("admin", version) || s.db.Version != 20 {
		t.Fatal("administrator not active")
	}
	reopened, err := openStore(s.dir)
	if err != nil || !reopened.adminSessionValid("admin", version) {
		t.Fatal("role did not survive reopen", err)
	}
	if err = reopened.setUserAdmin("admin", false, user.Revision); err != ErrConflict {
		t.Fatal("stale role update accepted", err)
	}
	if err = reopened.setUserAdmin("admin", false, user.Revision+1); err != nil {
		t.Fatal(err)
	}
	if reopened.adminSessionValid("admin", version) {
		t.Fatal("revoked role still active")
	}
}

func TestAdministratorSessionRequiresRoleAndIsRevoked(t *testing.T) {
	s := testStore(t)
	groups := []string{}
	password := "synthetic password"
	user, err := s.saveUser("admin", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", Sync: newSyncManager(s, newFakeGitHub(t, map[string]string{}).client()), AdminSessions: &readerSessions{}, AccessSessions: &readerSessions{}}
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
	if err := s.setUserAdmin("admin", true, user.Revision); err != nil {
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
	if err := s.setUserAdmin("admin", false, user.Revision+1); err != nil {
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
	w := Wiki{Store: s, ReaderLocal: true, LocalImportSource: "infra", AdminSessions: &readerSessions{}}
	provision := func(root bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/local/bootstrap-admin", strings.NewReader(`{"id":"operator"}`))
		if root {
			r = r.WithContext(context.WithValue(r.Context(), localRootKey{}, true))
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
	raw, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil || strings.Contains(string(raw), response.Token) {
		t.Fatal("setup token persisted in plaintext", err)
	}
	if _, ok := s.authenticateUser("operator", response.Token); ok {
		t.Fatal("setup token accepted as password")
	}
	oldToken := response.Token
	rotated := provision(true, "")
	if rotated.Code != 201 || json.Unmarshal(rotated.Body.Bytes(), &response) != nil || response.Token == oldToken {
		t.Fatal("pending setup could not be reissued")
	}
	if err := s.finishAdminSetup("operator", oldToken, "synthetic long password"); err == nil {
		t.Fatal("old setup token accepted")
	}
	if err := s.finishAdminSetup("operator", "wrong synthetic token", "synthetic long password"); err == nil {
		t.Fatal("wrong token accepted")
	}
	if err := s.finishAdminSetup("operator", response.Token, "synthetic long password"); err != nil {
		t.Fatal(err)
	}
	if err := s.finishAdminSetup("operator", response.Token, "different long password"); err == nil {
		t.Fatal("reused token accepted")
	}
	if provision(true, "").Code == 201 {
		t.Fatal("configured administrator replaced")
	}
	version, ok := s.authenticateUser("operator", "synthetic long password")
	if !ok || !s.adminSessionValid("operator", version) {
		t.Fatal("created administrator cannot login")
	}
}

func TestAdminSetupRequiresSecureSameOriginAndExpires(t *testing.T) {
	s := testStore(t)
	token, err := s.bootstrapAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, AdminSessions: &readerSessions{}}
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

func TestAdminSetupExpiredTokenRejected(t *testing.T) {
	s := testStore(t)
	token, err := s.bootstrapAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	u := s.db.Users["operator"]
	u.SetupExpires = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	s.db.Users["operator"] = u
	s.mu.Unlock()
	if err := s.finishAdminSetup("operator", token, "synthetic long password"); err == nil {
		t.Fatal("expired setup token accepted")
	}
}

func TestAdminBootstrapSurvivesSQLiteRestart(t *testing.T) {
	s := testStore(t)
	source(t, s, "fixture", "private")
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.bootstrapAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.finishAdminSetup("operator", token, "synthetic password"); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := s.authenticateUser("operator", "synthetic password")
	if !ok || !s.adminSessionValid("operator", v) {
		t.Fatal("admin login not restored")
	}
}

func TestOlderBinaryRefusesAdministratorStore(t *testing.T) {
	old := os.Getenv("VEDA_PRE_ADMIN_BINARY")
	if old == "" {
		t.Skip("pre-administrator binary not provided")
	}
	for _, sqlite := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sqlite"}[sqlite], func(t *testing.T) {
			s := testStore(t)
			source(t, s, "fixture", "private")
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
			if _, err := s.bootstrapAdmin("operator"); err != nil {
				t.Fatal(err)
			}
			name := "state.json"
			if sqlite {
				name = "state.sqlite"
			}
			file := filepath.Join(s.dir, name)
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			output, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(output), "unsupported store") {
				t.Fatal("old binary did not refuse administrator store")
			}
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("old binary changed store")
			}
		})
	}
}
