package main

import (
	"encoding/json"
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
