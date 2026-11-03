package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
)

func jsonResponse(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.WriteHeader(status)
	json.NewEncoder(rw).Encode(v)
}

func decodeBody(rw http.ResponseWriter, r *http.Request, dst any) error {
	return decodeLimited(rw, r, dst, 300000)
}

// decodeLimited decodes exactly one JSON value of at most limit bytes and
// refuses unknown fields.
func decodeLimited(rw http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(rw, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing input")
	}
	return nil
}

// apiError maps an operation error to its status and a fixed message; it
// never echoes the error text.
func apiError(rw http.ResponseWriter, err error) {
	if errors.Is(err, syncer.ErrLocalImportActive) {
		jsonResponse(rw, http.StatusConflict, map[string]string{"error": "local_import_active"})
		return
	}
	if errors.Is(err, importer.ErrAssetUnavailable) {
		jsonResponse(rw, http.StatusServiceUnavailable, map[string]string{"error": "asset_unavailable"})
		return
	}
	var upstream *github.RemoteError
	if errors.As(err, &upstream) {
		code := 502
		if upstream.Code == "repository_unavailable" {
			code = 404
		}
		if upstream.Code == "permission_denied" {
			code = 403
		}
		if upstream.Code == "rate_limited" || upstream.Code == "upstream_unavailable" {
			code = 503
		}
		jsonResponse(rw, code, map[string]string{"error": upstream.Code})
		return
	}
	code := 500
	message := "storage operation failed"
	if errors.Is(err, domain.ErrInvalid) {
		code = 400
		message = "invalid request"
	}
	if errors.Is(err, domain.ErrConflict) {
		code = 409
		message = "revision conflict"
	}
	if errors.Is(err, domain.ErrNotFound) {
		code = 404
		message = "not found"
	}
	jsonResponse(rw, code, map[string]string{"error": message})
}

// pagination reads the optional limit and offset query parameters of a list
// route. A limit outside 1..max or a negative offset is invalid.
func pagination(q url.Values, limit, max int) (int, int, bool) {
	offset := 0
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > max {
			return 0, 0, false
		}
		limit = n
	}
	if q.Has("offset") {
		n, err := strconv.Atoi(q.Get("offset"))
		if err != nil || n < 0 {
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}
