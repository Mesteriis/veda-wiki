package httpapi

import (
	"net/http"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/translation"
)

// maxTranslationBytes bounds a translation request body.
const maxTranslationBytes = 7 << 20

func (h handler) validateTranslation(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var in translation.Request
	if decodeLimited(rw, r, &in, maxTranslationBytes) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	result, err := h.Store.translation().Validate(in)
	if err != nil {
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 200, result)
}

func (h handler) translationNeeded(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	sourceID := r.URL.Query().Get("source_id")
	if sourceID != "" && !domain.IDPattern.MatchString(sourceID) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	limit, offset, ok := pagination(r.URL.Query(), 30, 100)
	if !ok {
		apiError(rw, domain.ErrInvalid)
		return
	}
	all := h.Store.translation().Candidates()
	if sourceID != "" {
		filtered := make([]translation.Candidate, 0)
		for _, candidate := range all {
			if candidate.SourceID == sourceID {
				filtered = append(filtered, candidate)
			}
		}
		all = filtered
	}
	start := min(offset, len(all))
	end := min(start+limit, len(all))
	jsonResponse(rw, 200, map[string]any{"candidates": all[start:end], "total": len(all), "offset": offset})
}

func (h handler) submitTranslation(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var in translation.Request
	if decodeLimited(rw, r, &in, maxTranslationBytes) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	tr, err := h.Store.translation().Submit(in)
	if err != nil {
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 200, tr)
}
