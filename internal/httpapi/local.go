package httpapi

import (
	"net/http"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
)

// localRoot reports a request to path from the local root user on the reader
// socket of a listener configured with a local import source. A browser
// request, which carries an Origin, never qualifies.
func (h handler) localRoot(r *http.Request, path string) bool {
	return h.ReaderLocal && h.LocalImportSource != "" && r.URL.Path == path && r.Method == "POST" && r.Context().Value(LocalRootKey{}) == true && r.Header.Get("Origin") == ""
}

// localRoute serves the routes reserved for root on the reader socket and
// reports whether it answered the request.
func (h handler) localRoute(rw http.ResponseWriter, r *http.Request) bool {
	switch {
	case h.Store != nil && h.localRoot(r, "/api/local/bootstrap-admin"):
		h.bootstrapAdmin(rw, r)
	case h.Store != nil && h.localRoot(r, "/api/local/createsuperuser"):
		h.createSuperuser(rw, r)
	case h.Store != nil && h.localRoot(r, "/api/local/remove-imported-source"):
		h.removeImportedSource(rw, r)
	case h.localRoot(r, "/api/v1/import"):
		h.localImport(rw, r)
	default:
		return false
	}
	return true
}

func (h handler) bootstrapAdmin(rw http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if decodeLimited(rw, r, &in, 1024) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	token, err := h.Store.auth().BootstrapAdmin(in.ID)
	if err != nil {
		apiError(rw, err)
		return
	}
	rw.Header().Set("Cache-Control", "no-store")
	jsonResponse(rw, 201, map[string]string{"setup_token": token})
}

func (h handler) createSuperuser(rw http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if decodeLimited(rw, r, &in, 2048) != nil {
		apiError(rw, domain.ErrInvalid)
		return
	}
	if err := h.Store.auth().CreateSuperuser(in.ID, in.Password); err != nil {
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 201, map[string]bool{"created": true})
}

func (h handler) removeImportedSource(rw http.ResponseWriter, r *http.Request) {
	var in struct {
		Generation string `json:"expected_generation"`
		Confirm    bool   `json:"confirm"`
	}
	if decodeLimited(rw, r, &in, 1024) != nil || !in.Confirm {
		apiError(rw, domain.ErrInvalid)
		return
	}
	count, err := h.Store.importer().RemoveLocalImportedSource(h.LocalImportSource, in.Generation)
	if err != nil {
		apiError(rw, err)
		return
	}
	jsonResponse(rw, 200, map[string]any{"source_id": h.LocalImportSource, "articles_removed": count})
}

// localImport applies an incremental import into the configured local source
// only; a complete import or another source is refused.
func (h handler) localImport(rw http.ResponseWriter, r *http.Request) {
	var batch importer.Batch
	if importer.Decode(rw, r, &batch) != nil || batch.SourceID != h.LocalImportSource || batch.Complete {
		apiError(rw, domain.ErrInvalid)
		return
	}
	result, err := h.Store.importer().Apply(batch, nil, true)
	if err != nil {
		apiError(rw, err)
		return
	}
	if h.Sync != nil && (result.Generation != *batch.ExpectedImport || result.Conflicts > 0) && h.Sync.HasEnabledMirror(batch.SourceID) {
		h.Sync.Wake()
	}
	jsonResponse(rw, 200, result)
}
