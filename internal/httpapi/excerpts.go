package httpapi

import (
	"net/http"
	"strconv"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// excerpt returns a bounded slice of an article's current text. Continuing
// past the first slice requires the expected hash, so a reader never mixes
// two revisions.
func (h handler) excerpt(rw http.ResponseWriter, r *http.Request, public bool) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	q := r.URL.Query()
	sourceID, p, expected := q.Get("source_id"), q.Get("path"), q.Get("expected_sha256")
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
	if offset > 0 && expected == "" {
		apiError(rw, domain.ErrInvalid)
		return
	}
	a, err := h.Store.article(sourceID, p, public)
	if err != nil {
		apiError(rw, err)
		return
	}
	revision, _ := domain.LastRevision(a)
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
