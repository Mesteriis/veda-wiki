package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareWikiPreviewIsReadOnlyAndScoped(t *testing.T) {
	batch := testBatch()
	batch.Files = map[string]string{"same.md": "# Same", "changed.md": "# Local", "new.md": "# New"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Error("comparison sent a write or credential")
		}
		switch r.URL.Path {
		case "/api/v1/sources":
			jsonResponse(w, 200, []Source{{ID: "handbook", Kind: "wiki"}, {ID: "other", Kind: "wiki"}})
		case "/api/v1/articles":
			jsonResponse(w, 200, []Page{
				{Path: "handbook/same.md", Environment: "handbook", SHA: digest([]byte("# Same"))},
				{Path: "handbook/changed.md", Environment: "handbook", SHA: digest([]byte("# Remote"))},
				{Path: "handbook/old.md", Environment: "handbook", SHA: digest([]byte("# Old"))},
				{Path: "other/new.md", Environment: "other", SHA: digest([]byte("# New"))},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result, err := compareWikiPreview(batch, server.URL)
	if err != nil || result["same"] != 1 || result["different"] != 1 || result["checkout_only"] != 1 || result["veda_only"] != 1 {
		t.Fatal("incorrect aggregate comparison", err, result)
	}
	batch.Kind = "program"
	if _, err = compareWikiPreview(batch, server.URL); err == nil {
		t.Fatal("program translation hashes compared as originals")
	}
}

func TestCompareWikiPreviewRefusesNonLoopbackRedirectAndMissingSource(t *testing.T) {
	batch := testBatch()
	if _, err := compareWikiPreview(batch, "http://192.0.2.1:8080"); err == nil {
		t.Fatal("remote URL accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1", http.StatusFound)
	}))
	defer server.Close()
	if _, err := compareWikiPreview(batch, server.URL); err == nil {
		t.Fatal("redirect accepted")
	}
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, []Source{}) }))
	defer missing.Close()
	if _, err := compareWikiPreview(batch, missing.URL); err != ErrNotFound {
		t.Fatal("missing source accepted", err)
	}
}

func TestCompareWikiPreviewRejectsDuplicatePaths(t *testing.T) {
	batch := testBatch()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sources" {
			jsonResponse(w, 200, []Source{{ID: "handbook", Kind: "wiki"}})
			return
		}
		page := Page{Path: "handbook/a.md", Environment: "handbook", SHA: digest([]byte("# A"))}
		jsonResponse(w, 200, []Page{page, page})
	}))
	defer server.Close()
	if _, err := compareWikiPreview(batch, server.URL); err != ErrInvalid {
		t.Fatal("duplicate page accepted", err)
	}
}
