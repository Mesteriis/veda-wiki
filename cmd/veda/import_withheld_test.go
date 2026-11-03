package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestImportWithheldCountIsScopedTransientAndOnlyUpdatedOnSuccess(t *testing.T) {
	s := testStore(t)
	batch := testBatch()
	batch.Withheld = 7
	if _, err := s.importBatch(batch); err != nil {
		t.Fatal(err)
	}
	source(t, s, "other", "private")
	key, err := s.createAccessKey("limited", []string{"handbook"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner"}
	contextRoute := "/api/v1/context?" + url.Values{"source_id": {"handbook"}, "path": {"README.md"}}.Encode()
	call := func(route string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", route, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	out := call(contextRoute)
	var context agentContext
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &context) != nil || context.SourceStatus.LastImportWithheld == nil || *context.SourceStatus.LastImportWithheld != 7 {
		t.Fatal("scoped context count missing", out.Code)
	}
	out = call("/api/state")
	var view struct {
		View View `json:"view"`
	}
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &view) != nil || len(view.View.SourceWarnings) != 1 || view.View.SourceWarnings[0].Withheld == nil || *view.View.SourceWarnings[0].Withheld != 7 || strings.Contains(out.Body.String(), "other") {
		t.Fatal("scoped warning incorrect", out.Code)
	}
	if call("/api/v1/context?source_id=other&path=guide.md").Code != 404 {
		t.Fatal("count escaped source scope")
	}
	batch = applyAgain(s, batch)
	batch.Withheld = 3
	if _, err = s.importBatch(batch); err != nil {
		t.Fatal(err)
	}
	if count := s.sources(false)[0].LastImportWithheld; count == nil || *count != 3 {
		t.Fatal("unchanged import did not update count")
	}
	stale := "stale"
	batch.ExpectedImport = &stale
	batch.Withheld = 9
	if _, err = s.importBatch(batch); err != ErrConflict {
		t.Fatal("stale import accepted", err)
	}
	if count := s.sources(false)[0].LastImportWithheld; count == nil || *count != 3 {
		t.Fatal("failed import changed count")
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range reopened.sources(false) {
		if src.LastImportWithheld != nil {
			t.Fatal("count incorrectly persisted")
		}
	}
}

func TestPublicViewsOmitImportWithheldCount(t *testing.T) {
	s := testStore(t)
	batch := testBatch()
	batch.Visibility = "public"
	if _, err := s.importBatch(batch); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Public: true}
	for _, route := range []string{"/api/v1/sources", "/api/v1/context?source_id=handbook&path=README.md", "/api/state"} {
		out := request(w, "GET", route, "", false)
		if out.Code != 200 || strings.Contains(out.Body.String(), "last_import_withheld") {
			t.Fatal("public count leaked", route, out.Code)
		}
	}
}
