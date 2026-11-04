package httpapi

import (
	"net/http"
	"strconv"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// excerpt returns a bounded slice of an article's current text, or of its
// displayed text on the display route. Continuing past the first slice
// requires the expected hashes, so a reader never mixes two revisions.
func (h handler) excerpt(rw http.ResponseWriter, r *http.Request, public bool) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	q := r.URL.Query()
	sourceID, p, expected := q.Get("source_id"), q.Get("path"), q.Get("expected_sha256")
	display := r.URL.Path == "/api/v1/articles/display-excerpt"
	expectedOriginal := q.Get("expected_original_sha256")
	if !domain.ValidID(sourceID) || p == "" || (expected != "" && len(expected) != 64) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	if expected != "" {
		if _, err := domain.HexKey(expected); err != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
	}
	if display && expectedOriginal != "" {
		if _, err := domain.HexKey(expectedOriginal); err != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
	}
	offset, limit := 0, 8000
	if q.Has("offset") {
		n, err := strconv.Atoi(q.Get("offset"))
		if err != nil || n < 0 {
			apiError(rw, domain.ErrInvalid)
			return
		}
		offset = n
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 16000 {
			apiError(rw, domain.ErrInvalid)
			return
		}
		limit = n
	}
	if offset > 0 && (expected == "" || (display && expectedOriginal == "")) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	if display {
		page, err := h.Store.page(sourceID+"/"+p, public)
		if err != nil {
			apiError(rw, err)
			return
		}
		if (expected != "" && page.SHA != expected) || (expectedOriginal != "" && page.OriginalSHA != expectedOriginal) {
			apiError(rw, domain.ErrConflict)
			return
		}
		text := []rune(page.Text)
		if offset > len(text) {
			apiError(rw, domain.ErrInvalid)
			return
		}
		end := offset + min(limit, len(text)-offset)
		var next *int
		if end < len(text) {
			next = &end
		}
		jsonResponse(rw, 200, map[string]any{
			"source_id":             sourceID,
			"path":                  p,
			"sha256":                page.SHA,
			"original_sha256":       page.OriginalSHA,
			"translated":            page.Translated,
			"translation_state":     page.TranslationState,
			"translation_origin":    page.TranslationOrigin,
			"program_documentation": page.ProgramDocumentation,
			"offset":                offset,
			"next_offset":           next,
			"total_characters":      len(text),
			"text":                  string(text[offset:end]),
		})
		return
	}
	a, err := h.Store.article(sourceID, p, public)
	if err != nil {
		apiError(rw, err)
		return
	}
	revision := a.Versions[len(a.Versions)-1]
	if expected != "" && revision.SHA != expected {
		apiError(rw, domain.ErrConflict)
		return
	}
	text := []rune(revision.Text)
	if offset > len(text) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	end := offset + min(limit, len(text)-offset)
	var next *int
	if end < len(text) {
		next = &end
	}
	jsonResponse(rw, 200, map[string]any{
		"source_id":        sourceID,
		"path":             p,
		"revision":         revision.Number,
		"sha256":           revision.SHA,
		"offset":           offset,
		"next_offset":      next,
		"total_characters": len(text),
		"text":             string(text[offset:end]),
	})
}
