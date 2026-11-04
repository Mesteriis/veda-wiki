package httpapi

import (
	"net/http"
	"strconv"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
)

// conflicts lists unresolved import conflicts without their incoming text,
// or returns one conflict in full when it is named.
func (h handler) conflicts(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	all := h.Store.importer().Conflicts()
	query := r.URL.Query()
	if query.Get("source_id") != "" {
		index, err := strconv.Atoi(query.Get("index"))
		if err != nil || index < 0 {
			apiError(rw, domain.ErrInvalid)
			return
		}
		for _, record := range all {
			if record.SourceID == query.Get("source_id") && record.Path == query.Get("path") && record.Index == index {
				jsonResponse(rw, 200, map[string]any{"conflicts": []importer.ConflictRecord{record}, "total": 1, "offset": 0})
				return
			}
		}
		apiError(rw, domain.ErrNotFound)
		return
	}
	limit, offset, ok := pagination(query, 30, 100)
	if !ok {
		apiError(rw, domain.ErrInvalid)
		return
	}
	start := min(offset, len(all))
	end := min(start+limit, len(all))
	rows := all[start:end]
	for i := range rows {
		rows[i].Conflict.Incoming = ""
	}
	jsonResponse(rw, 200, map[string]any{"conflicts": rows, "total": len(all), "offset": offset})
}

func (h handler) resolveConflict(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var in struct {
		SourceID   string `json:"source_id"`
		Path       string `json:"path"`
		Index      *int   `json:"index"`
		Expected   int    `json:"expected_revision"`
		Resolution string `json:"resolution"`
	}
	if err := decodeBody(rw, r, &in); err != nil || in.Index == nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	if err := h.Store.importer().ResolveConflict(in.SourceID, in.Path, *in.Index, in.Expected, in.Resolution); err != nil {
		apiError(rw, err)
		return
	}
	if h.Sync != nil {
		h.Sync.RetryConnection(in.SourceID)
		h.Sync.Wake()
	}
	jsonResponse(rw, 200, map[string]string{"status": "resolved"})
}

// importBatch applies a repository import from the owner.
func (h handler) importBatch(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var batch importer.Batch
	if err := importer.Decode(rw, r, &batch); err != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	result, err := h.Store.importer().Import(batch)
	if err != nil {
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 200, result)
}
