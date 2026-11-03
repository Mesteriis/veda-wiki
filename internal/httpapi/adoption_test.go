package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

type adoptionDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *adoptionDeadlineWriter) SetWriteDeadline(t time.Time) error { w.deadline = t; return nil }

func TestAdoptRequestExtendsWriteDeadline(t *testing.T) {
	s := testStore(t)
	m := newSyncManager(s, githubtest.New(t, map[string]string{"veda.yaml": metadataFixture}).Client())
	m.LocalImportSource = "handbook"
	w := Wiki{Store: s, Token: "test-owner", Sync: m}
	r := httptest.NewRequest("POST", "/api/v1/github/connections", strings.NewReader(`{"id":"handbook","repository":"org/handbook","account_id":"operator","adopt_existing":true}`))
	r.Header.Set("Authorization", "Bearer test-owner")
	out := &adoptionDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	w.ServeHTTP(out, r)
	remaining := time.Until(out.deadline)
	if out.Code != 409 || remaining < 5*time.Minute || remaining > 6*time.Minute {
		t.Fatal("adoption write deadline not extended", out.Code, remaining)
	}
}

func TestAdoptThroughOwnerAPI(t *testing.T) {
	s := testStore(t)
	if _, err := s.importBatch(testBatch()); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Remote"})
	f.Modify(func() { f.Private = true })
	m := newSyncManager(s, f.Client())
	if _, err := s.saveAccount(context.Background(), f.Client(), "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "test-owner", Sync: m}
	body := `{"id":"handbook","repository":"org/handbook","account_id":"operator","adopt_existing":true}`
	if r := request(w, "POST", "/api/v1/github/connections", body, false); r.Code != 403 {
		t.Fatal("unauthorized adoption", r.Code)
	}
	if r := request(w, "POST", "/api/v1/github/connections", body, true); r.Code != 202 {
		t.Fatal("owner adoption failed", r.Code)
	}
	if s.DB().Sources["handbook"].ConnectionID != "handbook" || len(s.DB().Articles["handbook/README.md"].Conflicts) != 1 {
		t.Fatal("owner API did not preserve conflict")
	}
}

func TestAdoptConfiguredLocalImporterIsRejected(t *testing.T) {
	s := testStore(t)
	if _, err := s.importBatch(testBatch()); err != nil {
		t.Fatal(err)
	}
	m := newSyncManager(s, githubtest.New(t, map[string]string{"veda.yaml": metadataFixture}).Client())
	m.LocalImportSource = "handbook"
	w := Wiki{Store: s, Token: "test-owner", Sync: m}
	body := `{"id":"handbook","repository":"org/handbook","account_id":"operator","adopt_existing":true}`
	r := request(w, "POST", "/api/v1/github/connections", body, true)
	if r.Code != 409 || !strings.Contains(r.Body.String(), "local_import_active") || len(s.connections()) != 0 {
		t.Fatal("configured importer not protected", r.Code)
	}
}
