package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Mesteriis/veda-wiki/internal/agentctx"
	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// maxContextBytes bounds the encoded agent context response.
const maxContextBytes = 2000000

// context returns the AGENTS.md context that applies to a path.
func (h handler) context(rw http.ResponseWriter, r *http.Request, public bool) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	sourceID, target := r.URL.Query().Get("source_id"), r.URL.Query().Get("path")
	if !domain.IDPattern.MatchString(sourceID) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	result, err := agentctx.Context(h.Store.Store, h.Store.observations, sourceID, target, public)
	if errors.Is(err, agentctx.ErrContextLimit) {
		jsonResponse(rw, 413, map[string]string{"error": "context exceeds response limit"})
		return
	}
	if err != nil {
		apiError(rw, err)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		apiError(rw, err)
		return
	}
	if len(encoded) >= maxContextBytes {
		jsonResponse(rw, 413, map[string]string{"error": "context exceeds response limit"})
		return
	}
	jsonResponse(rw, 200, result)
}

// proposalBase returns what an agent needs to propose a change to an existing
// article, or with create to a new one.
func (h handler) proposalBase(rw http.ResponseWriter, r *http.Request, create bool) {
	if r.Method != "GET" {
		rw.WriteHeader(405)
		return
	}
	sourceID, p := r.URL.Query().Get("source_id"), r.URL.Query().Get("path")
	if !domain.IDPattern.MatchString(sourceID) || !domain.SafeArticlePath(p) {
		apiError(rw, domain.ErrInvalid)
		return
	}
	base := agentctx.ProposalBase
	if create {
		base = agentctx.NewProposalBase
	}
	result, err := base(h.Store.Store, h.Store.observations, sourceID, p)
	if err != nil {
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 200, result)
}
