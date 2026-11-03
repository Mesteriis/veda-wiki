package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalImportIsRestrictedAndPreservesConflicts(t *testing.T) {
	s := testStore(t)
	initial := testBatch()
	if _, e := s.importBatch(initial); e != nil {
		t.Fatal(e)
	}
	src := s.DB().Sources["handbook"]
	gen := src.ImportGeneration
	next := testBatch()
	next.Create = false
	next.Visibility = ""
	next.Complete = false
	next.ExpectedImport = &gen
	next.Files["README.md"] = "# Upstream update"
	if _, e := s.put("handbook", "README.md", "# Local edit", 1); e != nil {
		t.Fatal(e)
	}
	w := Wiki{Store: s, ReaderLocal: true, LocalImportSource: "handbook"}
	call := func(batch ImportBatch, root bool, origin string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(batch)
		r := httptest.NewRequest("POST", "/api/v1/import", strings.NewReader(string(b)))
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
	if r := call(next, false, ""); r.Code != 403 {
		t.Fatal("non-root import accepted", r.Code)
	}
	if r := call(next, true, "http://localhost"); r.Code != 403 {
		t.Fatal("browser import accepted", r.Code)
	}
	complete := next
	complete.Complete = true
	if r := call(complete, true, ""); r.Code != 400 {
		t.Fatal("complete import accepted", r.Code)
	}
	wrong := next
	wrong.SourceID = "other"
	if r := call(wrong, true, ""); r.Code != 400 {
		t.Fatal("other source accepted", r.Code)
	}
	if r := call(next, true, ""); r.Code != 200 {
		t.Fatal("allowed import failed", r.Code)
	}
	a, e := s.article("handbook", "README.md", false)
	if e != nil || a.Versions[len(a.Versions)-1].Text != "# Local edit" || len(a.Conflicts) != 1 {
		t.Fatal("local edit not protected", e)
	}
	if r := call(next, true, ""); r.Code != 409 {
		t.Fatal("stale generation accepted", r.Code)
	}
}
func TestLocalImportRefusesPublicOrConnectedSources(t *testing.T) {
	for _, visibility := range []string{"public", "private"} {
		s := testStore(t)
		batch := testBatch()
		batch.Visibility = visibility
		if _, e := s.importBatch(batch); e != nil {
			t.Fatal(e)
		}
		if visibility == "private" {
			s.Lock()
			v := s.DB().Sources["handbook"]
			v.ConnectionID = "handbook"
			s.DB().Sources[v.ID] = v
			s.Unlock()
		}
		gen := s.DB().Sources["handbook"].ImportGeneration
		batch.Create = false
		batch.Complete = false
		batch.ExpectedImport = &gen
		if _, e := s.applyImport(batch, nil, true); e != ErrInvalid {
			t.Fatal("public or connected source accepted", visibility, e)
		}
	}
}
