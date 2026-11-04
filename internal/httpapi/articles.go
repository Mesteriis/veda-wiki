package httpapi

import (
	"net/http"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// articles lists the visible pages, reads one article or writes a revision.
// Only the owner receives history and conflicts; a reader never receives
// copy, reconciliation or publication bookkeeping.
func (h handler) articles(rw http.ResponseWriter, r *http.Request, owner, reader bool) {
	switch r.Method {
	case "GET":
		sourceID, p := r.URL.Query().Get("source_id"), r.URL.Query().Get("path")
		if sourceID == "" && p == "" {
			jsonResponse(rw, 200, h.Store.snapshot(!owner && !reader).Pages)
			return
		}
		a, err := h.Store.article(sourceID, p, !owner && !reader)
		if err != nil {
			apiError(rw, err)
			return
		}
		if r.URL.Query().Get("history") != "1" || !owner {
			if last, ok := domain.LastRevision(a); ok {
				a.Versions = []domain.Revision{last}
			}
			a.Conflicts = nil
		}
		if reader && !owner {
			a.CopiedFrom = nil
			a.ReconcileBase = nil
			a.Published = nil
			a.PublishedLocal = false
			a.UpstreamHash = ""
		}
		jsonResponse(rw, 200, a)
	case "POST", "PUT":
		var input struct {
			SourceID string `json:"source_id"`
			Path     string `json:"path"`
			Text     string `json:"text"`
			Expected *int   `json:"expected_revision"`
		}
		if err := decodeLimited(rw, r, &input, 7<<20); err != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		if input.Expected == nil || (r.Method == "POST" && *input.Expected != 0) || (r.Method == "PUT" && *input.Expected < 1) {
			apiError(rw, domain.ErrInvalid)
			return
		}
		rev, err := h.Store.put(input.SourceID, input.Path, input.Text, *input.Expected)
		if err != nil {
			apiError(rw, err)
			return
		}
		if h.Sync != nil {
			h.Sync.Wake()
		}
		status := 200
		if r.Method == "POST" {
			status = 201
		}
		jsonResponse(rw, status, rev)
	default:
		rw.WriteHeader(405)
	}
}

func (h handler) search(rw http.ResponseWriter, r *http.Request, public bool) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	sourceID := r.URL.Query().Get("source_id")
	if query == "" || len(query) > 300 || (sourceID != "" && !domain.ValidID(sourceID)) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	limit, offset, ok := pagination(r.URL.Query(), 20, 100)
	if !ok {
		apiError(rw, domain.ErrInvalid)
		return
	}
	all := h.Store.findArticles(query, sourceID, public)
	start := min(offset, len(all))
	end := start + min(limit, len(all)-start)
	jsonResponse(rw, 200, map[string]any{"results": all[start:end], "total": len(all), "offset": offset})
}
