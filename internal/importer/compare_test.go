package importer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestCompareWikiPreviewIsReadOnlyAndScoped(t *testing.T) {
	batch := testBatch()
	batch.Files = map[string]string{"same.md": "# Same", "changed.md": "# Local", "new.md": "# New"}
	batch.Assets = map[string][]byte{"same.png": []byte("same"), "changed.png": []byte("local"), "new.png": []byte("new")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Error("comparison sent a write or credential")
		}
		switch r.URL.Path {
		case "/api/v1/sources":
			jsonResponse(w, 200, []domain.Source{{ID: "handbook", Kind: "wiki", Assets: map[string]domain.BlobRef{
				"same.png": {SHA: domain.Digest([]byte("same"))}, "changed.png": {SHA: domain.Digest([]byte("remote"))}, "old.png": {SHA: domain.Digest([]byte("old"))},
			}}, {ID: "other", Kind: "wiki", Assets: map[string]domain.BlobRef{"new.png": {SHA: domain.Digest([]byte("new"))}}}})
		case "/api/v1/articles":
			jsonResponse(w, 200, []domain.Page{
				{Path: "handbook/same.md", Environment: "handbook", SHA: domain.Digest([]byte("# Same"))},
				{Path: "handbook/changed.md", Environment: "handbook", SHA: domain.Digest([]byte("# Remote"))},
				{Path: "handbook/old.md", Environment: "handbook", SHA: domain.Digest([]byte("# Old"))},
				{Path: "other/new.md", Environment: "other", SHA: domain.Digest([]byte("# New"))},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result, err := ComparePreview(batch, server.URL)
	if err != nil || result.Articles["same"] != 1 || result.Articles["different"] != 1 || result.Articles["checkout_only"] != 1 || result.Articles["veda_only"] != 1 || result.Assets["same"] != 1 || result.Assets["different"] != 1 || result.Assets["checkout_only"] != 1 || result.Assets["veda_only"] != 1 {
		t.Fatal("incorrect aggregate comparison", err, result)
	}
	batch.Kind = "program"
	if _, err = ComparePreview(batch, server.URL); err == nil {
		t.Fatal("program translation hashes compared as originals")
	}
}

func TestCompareWikiPreviewRefusesNonLoopbackRedirectAndMissingSource(t *testing.T) {
	batch := testBatch()
	if _, err := ComparePreview(batch, "http://192.0.2.1:8080"); err == nil {
		t.Fatal("remote URL accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1", http.StatusFound)
	}))
	defer server.Close()
	if _, err := ComparePreview(batch, server.URL); err == nil {
		t.Fatal("redirect accepted")
	}
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, []domain.Source{}) }))
	defer missing.Close()
	if _, err := ComparePreview(batch, missing.URL); err != domain.ErrNotFound {
		t.Fatal("missing source accepted", err)
	}
}

func TestCompareWikiPreviewRejectsDuplicatePaths(t *testing.T) {
	batch := testBatch()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/sources" {
			jsonResponse(w, 200, []domain.Source{{ID: "handbook", Kind: "wiki"}})
			return
		}
		page := domain.Page{Path: "handbook/a.md", Environment: "handbook", SHA: domain.Digest([]byte("# A"))}
		jsonResponse(w, 200, []domain.Page{page, page})
	}))
	defer server.Close()
	if _, err := ComparePreview(batch, server.URL); err != domain.ErrInvalid {
		t.Fatal("duplicate page accepted", err)
	}
}

func TestCompareWikiPreviewRejectsInvalidAssetReference(t *testing.T) {
	batch := testBatch()
	for _, assets := range []map[string]domain.BlobRef{
		{"../outside.png": {SHA: domain.Digest([]byte("image"))}},
		{"image.png": {SHA: "invalid"}},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/sources" {
				jsonResponse(w, 200, []domain.Source{{ID: "handbook", Kind: "wiki", Assets: assets}})
				return
			}
			jsonResponse(w, 200, []domain.Page{})
		}))
		_, err := ComparePreview(batch, server.URL)
		server.Close()
		if err != domain.ErrInvalid {
			t.Fatal("invalid asset reference accepted", err)
		}
	}
}

// jsonResponse writes v as a VEDA API response would.
func jsonResponse(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(status)
	json.NewEncoder(rw).Encode(v)
}
