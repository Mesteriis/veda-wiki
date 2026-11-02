package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImportObservationIsTransientAndOnlyAdvancesOnSuccess(t *testing.T) {
	s := testStore(t)
	batch := testBatch()
	batch.Visibility = "public"
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	source := s.sources(false)[0]
	if source.LastImportObservedAt == "" {
		t.Fatal("successful import not observed")
	}
	if public := s.sources(true)[0]; public.LastImportObservedAt != "" || public.UnresolvedConflicts != 0 {
		t.Fatal("public observation metadata exposed")
	}
	old := time.Now().Add(-time.Hour).UTC()
	s.mu.Lock()
	s.observed["handbook"] = old
	s.mu.Unlock()
	batch.Create = false
	gen := s.db.Sources["handbook"].ImportGeneration
	batch.ExpectedImport = &gen
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	observed := s.sources(false)[0].LastImportObservedAt
	parsed, e := time.Parse(time.RFC3339Nano, observed)
	if e != nil || !parsed.After(old) {
		t.Fatal("unchanged import did not refresh observation", e)
	}
	stale := "not-the-current-generation"
	batch.ExpectedImport = &stale
	if _, e := s.importBatch(batch); e != ErrConflict {
		t.Fatal("stale generation accepted", e)
	}
	if s.sources(false)[0].LastImportObservedAt != observed {
		t.Fatal("failed import advanced observation")
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	if reopened.sources(false)[0].LastImportObservedAt != "" {
		t.Fatal("observation should be unknown after restart")
	}
}
func TestContextSourceStatusCountsUnresolvedConflicts(t *testing.T) {
	s := testStore(t)
	batch := testBatch()
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	if _, e := s.put("handbook", "README.md", "# Local", 1); e != nil {
		t.Fatal(e)
	}
	batch = applyAgain(s, batch)
	batch.Files["README.md"] = "# Upstream"
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	view := s.sources(false)[0]
	if view.UnresolvedConflicts != 1 {
		t.Fatal("missing unresolved conflict count")
	}
	w := Wiki{Store: s, Token: "test-owner"}
	r := request(w, "GET", "/api/v1/sources", "", true)
	var response []map[string]any
	if e := json.Unmarshal(r.Body.Bytes(), &response); e != nil || len(response) != 1 || response[0]["unresolved_conflicts"] != float64(1) || !strings.Contains(r.Body.String(), "last_import_observed_at") {
		t.Fatal("source status response", e)
	}
}

func TestStateShowsWarningsOnlyForVisiblePrivateSources(t *testing.T) {
	s := testStore(t)
	source(t, s, "alpha", "private")
	source(t, s, "beta", "private")
	source(t, s, "open", "public")
	for _, id := range []string{"alpha", "beta", "open"} {
		if _, err := s.put(id, "a.md", "# Synthetic", 0); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	db := s.clone()
	for _, id := range []string{"alpha", "beta", "open"} {
		src := db.Sources[id]
		src.ImportPartial = true
		db.Sources[id] = src
	}
	err := s.commit(db)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.createAccessKey("reader", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	read := func(token string) []SourceWarning {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/state", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, req)
		var payload struct {
			View View `json:"view"`
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &payload) != nil {
			t.Fatal("state failed", out.Code)
		}
		return payload.View.SourceWarnings
	}
	if got := read(""); len(got) != 0 {
		t.Fatal("public state exposed warnings")
	}
	if got := read(key); len(got) != 1 || got[0].ID != "alpha" || !got[0].Partial {
		t.Fatal("scoped warnings were missing or leaked")
	}
	if got := read("owner"); len(got) != 3 {
		t.Fatal("owner did not receive all visible warnings")
	}
}
