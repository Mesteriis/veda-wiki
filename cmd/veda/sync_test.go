package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func connectFixture(t *testing.T, s *Store, f *githubtest.Fake, write bool) *SyncManager {
	t.Helper()
	m := newSyncManager(s, f.Client())
	account := ""
	if write || f.Private {
		if _, e := s.saveAccount(context.Background(), f.Client(), "operator", github.Secret{Kind: "token", Token: githubtest.Access}); e != nil {
			t.Fatal(e)
		}
		account = "operator"
	}
	if _, e := m.Connect(context.Background(), connectRequest{ID: "handbook", Repository: "https://github.com/org/handbook", AccountID: account, Write: write}); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	return m
}

func TestGitHubManagementAPIProtectsCredentials(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Public"})
	m := newSyncManager(s, f.Client())
	w := Wiki{Store: s, Token: "test-owner", Sync: m}
	body := `{"id":"operator","kind":"token","token":"` + githubtest.Access + `"}`
	if r := request(w, "POST", "/api/v1/github/accounts", body, true); r.Code != 403 {
		t.Fatal("LAN HTTP credentials accepted")
	}
	r := httptest.NewRequest("POST", "/api/v1/github/accounts", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Authorization", "Bearer test-owner")
	response := httptest.NewRecorder()
	w.ServeHTTP(response, r)
	if response.Code != 201 || strings.Contains(response.Body.String(), githubtest.Access) || strings.Contains(response.Body.String(), "secret_ref") {
		t.Fatal(response.Code, response.Body)
	}
	for _, route := range []string{"/api/v1/github/accounts", "/api/v1/github/connections", "/api/v1/github/outbox"} {
		if r := request(w, "GET", route, "", false); r.Code != 404 {
			t.Fatal(route, r.Code)
		}
	}
	if r := request(w, "POST", "/api/v1/github/connections", `{"id":"handbook","repository":"org/handbook","account_id":"operator","write":true}`, true); r.Code != 202 {
		t.Fatal(r.Code, r.Body)
	}
	m.Once(context.Background())
	if r := request(w, "GET", "/api/v1/github/outbox?limit=0", "", true); r.Code != 400 {
		t.Fatal(r.Code)
	}
	w.Public = true
	if r := request(w, "GET", "/api/v1/github/accounts", "", true); r.Code != 404 {
		t.Fatal("public listener accepted owner key")
	}
}
