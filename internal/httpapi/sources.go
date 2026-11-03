package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
)

// sources lists the visible sources or adds a local one.
func (h handler) sources(rw http.ResponseWriter, r *http.Request, owner, reader bool) {
	switch r.Method {
	case "GET":
		jsonResponse(rw, 200, h.Store.sources(!owner && !reader))
	case "POST":
		var src domain.Source
		if err := decodeBody(rw, r, &src); err != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		if err := h.Store.addSource(src); err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 201, src)
	default:
		rw.WriteHeader(405)
	}
}

// syncer returns the running manager, or one without a GitHub client for
// mirror and reconciliation requests to a handler that runs no manager.
func (h handler) syncer() *syncer.Manager {
	if h.Sync != nil {
		return h.Sync
	}
	return h.Store.syncer()
}

func (h handler) mirrors(rw http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		jsonResponse(rw, 200, h.syncer().Mirrors())
		return
	}
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var in syncer.MirrorRequest
	if decodeBody(rw, r, &in) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	if in.Enabled != nil && *in.Enabled && h.Sync == nil {
		jsonResponse(rw, http.StatusServiceUnavailable, map[string]string{"error": "sync_unavailable"})
		return
	}
	_ = http.NewResponseController(rw).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	mirror, err := h.syncer().ConfigureMirror(ctx, in)
	if err != nil {
		apiError(rw, err)
		return
	}
	if h.Sync != nil && mirror.Enabled {
		h.Sync.Wake()
	}
	jsonResponse(rw, 200, mirror)
}

func (h handler) reconcile(rw http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	var in syncer.ReconcileRequest
	if decodeBody(rw, r, &in) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	_ = http.NewResponseController(rw).SetWriteDeadline(time.Now().Add(2 * time.Minute))
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	result, err := h.syncer().ReconcileSources(ctx, in)
	if err != nil {
		apiError(rw, err)
		return
	}
	if h.Sync != nil {
		h.Sync.Wake()
	}
	jsonResponse(rw, 200, result)
}
