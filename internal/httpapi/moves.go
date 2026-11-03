package httpapi

import (
	"net/http"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

func (h handler) moves(rw http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		all := h.Store.transfer().Moves()
		limit, offset, ok := pagination(r.URL.Query(), 30, 100)
		if !ok {
			apiError(rw, domain.ErrInvalid)
			return
		}
		start := min(offset, len(all))
		end := min(start+limit, len(all))
		jsonResponse(rw, 200, map[string]any{"moves": all[start:end], "total": len(all), "offset": offset})
		return
	}
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var in transfer.MoveRequest
	if decodeBody(rw, r, &in) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	result, err := h.Store.transfer().Move(in)
	if err != nil {
		apiError(rw, err)
		return
	}
	if h.Sync != nil {
		h.Sync.Wake()
	}
	jsonResponse(rw, 201, result)
}

func (h handler) cancelMove(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var in struct {
		ID      string `json:"id"`
		Confirm bool   `json:"confirm_cancel"`
	}
	if decodeBody(rw, r, &in) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	if err := h.Store.transfer().CancelMove(in.ID, in.Confirm); err != nil {
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 200, map[string]string{"state": "cancelled"})
}

func (h handler) copyArticle(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var in transfer.CopyRequest
	if err := decodeBody(rw, r, &in); err != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	rev, err := h.Store.transfer().Copy(in)
	if err != nil {
		apiError(rw, err)
		return
	}
	if h.Sync != nil {
		h.Sync.Wake()
	}
	jsonResponse(rw, 201, rev)
}

// transferPlan previews a copy or move without changing anything; readers
// may call it for the sources they can read.
func (h handler) transferPlan(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var in transfer.CopyRequest
	if decodeBody(rw, r, &in) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	plan, err := h.Store.transfer().PlanTransfer(in)
	if err != nil {
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 200, plan)
}
