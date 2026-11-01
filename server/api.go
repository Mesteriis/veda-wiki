package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (w Wiki) authorized(r *http.Request) bool {
	if w.Public || w.Token == "" {
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(provided), []byte(w.Token)) == 1
}

// A reader credential grants current content only. It never grants writes or
// administrative APIs, even when presented to the private listener.
func (w Wiki) readerAuthorized(r *http.Request) bool {
	if w.Public || w.ReaderToken == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(provided), []byte(w.ReaderToken)) == 1
}
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
func apiError(rw http.ResponseWriter, err error) {
	var upstream *remoteError
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
	if errors.Is(err, ErrInvalid) {
		code = 400
		message = "invalid request"
	}
	if errors.Is(err, ErrConflict) {
		code = 409
		message = "revision conflict"
	}
	if errors.Is(err, ErrNotFound) {
		code = 404
		message = "not found"
	}
	jsonResponse(rw, code, map[string]string{"error": message})
}
func (w Wiki) api(rw http.ResponseWriter, r *http.Request, owner, reader bool) {
	if r.Method != "GET" && (!owner || r.Header.Get("Origin") != "") {
		jsonResponse(rw, 403, map[string]string{"error": "owner authorization required; browser writes are disabled"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/github/") {
		w.githubAPI(rw, r, owner)
		return
	}
	switch r.URL.Path {
	case "/api/v1/moves/cancel":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in struct {
			ID      string `json:"id"`
			Confirm bool   `json:"confirm_cancel"`
		}
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		if err := w.Store.cancelMove(in.ID, in.Confirm); err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, map[string]string{"state": "cancelled"})
	case "/api/v1/moves":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method == "GET" {
			all := w.Store.moves()
			limit, offset := 30, 0
			if r.URL.Query().Has("limit") {
				n, e := strconv.Atoi(r.URL.Query().Get("limit"))
				if e != nil || n < 1 || n > 100 {
					apiError(rw, ErrInvalid)
					return
				}
				limit = n
			}
			if r.URL.Query().Has("offset") {
				n, e := strconv.Atoi(r.URL.Query().Get("offset"))
				if e != nil || n < 0 {
					apiError(rw, ErrInvalid)
					return
				}
				offset = n
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
		var in moveRequest
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		result, e := w.Store.moveArticle(in)
		if e != nil {
			apiError(rw, e)
			return
		}
		if w.Sync != nil {
			w.Sync.Wake()
		}
		jsonResponse(rw, 201, result)
	case "/api/v1/articles/translation-needed":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		limit, offset := 30, 0
		if r.URL.Query().Has("limit") {
			n, e := strconv.Atoi(r.URL.Query().Get("limit"))
			if e != nil || n < 1 || n > 100 {
				apiError(rw, ErrInvalid)
				return
			}
			limit = n
		}
		if r.URL.Query().Has("offset") {
			n, e := strconv.Atoi(r.URL.Query().Get("offset"))
			if e != nil || n < 0 {
				apiError(rw, ErrInvalid)
				return
			}
			offset = n
		}
		all := w.Store.translationCandidates()
		start := min(offset, len(all))
		end := min(start+limit, len(all))
		jsonResponse(rw, 200, map[string]any{"candidates": all[start:end], "total": len(all), "offset": offset})
	case "/api/v1/articles/translation":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in translationRequest
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		tr, e := w.Store.submitTranslation(in)
		if e != nil {
			apiError(rw, e)
			return
		}
		jsonResponse(rw, 200, tr)
	case "/api/v1/articles/copy":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in copyRequest
		if err := decodeBody(rw, r, &in); err != nil {
			apiError(rw, ErrInvalid)
			return
		}
		rev, err := w.Store.copyArticle(in)
		if err != nil {
			apiError(rw, err)
			return
		}
		if w.Sync != nil {
			w.Sync.Wake()
		}
		jsonResponse(rw, 201, rev)
	case "/api/v1/conflicts":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		all := w.Store.conflicts()
		query := r.URL.Query()
		if query.Get("source_id") != "" {
			index, e := strconv.Atoi(query.Get("index"))
			if e != nil || index < 0 {
				apiError(rw, ErrInvalid)
				return
			}
			for _, record := range all {
				if record.SourceID == query.Get("source_id") && record.Path == query.Get("path") && record.Index == index {
					jsonResponse(rw, 200, map[string]any{"conflicts": []ConflictRecord{record}, "total": 1, "offset": 0})
					return
				}
			}
			apiError(rw, ErrNotFound)
			return
		}
		limit, offset := 30, 0
		if query.Has("limit") {
			value, e := strconv.Atoi(query.Get("limit"))
			if e != nil || value < 1 || value > 100 {
				apiError(rw, ErrInvalid)
				return
			}
			limit = value
		}
		if query.Has("offset") {
			value, e := strconv.Atoi(query.Get("offset"))
			if e != nil || value < 0 {
				apiError(rw, ErrInvalid)
				return
			}
			offset = value
		}
		start := offset
		if start > len(all) {
			start = len(all)
		}
		end := start + limit
		if end > len(all) {
			end = len(all)
		}
		rows := all[start:end]
		for i := range rows {
			rows[i].Conflict.Incoming = ""
		}
		jsonResponse(rw, 200, map[string]any{"conflicts": rows, "total": len(all), "offset": offset})
	case "/api/v1/conflicts/resolve":
		if !owner {
			http.NotFound(rw, r)
			return
		}
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
		if e := decodeBody(rw, r, &in); e != nil || in.Index == nil {
			apiError(rw, ErrInvalid)
			return
		}
		if e := w.Store.resolveConflict(in.SourceID, in.Path, *in.Index, in.Expected, in.Resolution); e != nil {
			apiError(rw, e)
			return
		}
		if w.Sync != nil {
			w.Store.retryConnection(in.SourceID)
			w.Sync.Wake()
		}
		jsonResponse(rw, 200, map[string]string{"status": "resolved"})
	case "/api/v1/import":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var batch ImportBatch
		if e := decodeLimited(rw, r, &batch, 40<<20); e != nil {
			apiError(rw, ErrInvalid)
			return
		}
		result, e := w.Store.importBatch(batch)
		if e != nil {
			apiError(rw, e)
			return
		}
		jsonResponse(rw, 200, result)
	case "/api/v1/sources":
		switch r.Method {
		case "GET":
			jsonResponse(rw, 200, w.Store.sources(!owner && !reader))
		case "POST":
			var src Source
			if err := decodeBody(rw, r, &src); err != nil {
				apiError(rw, ErrInvalid)
				return
			}
			if err := w.Store.addSource(src); err != nil {
				apiError(rw, err)
				return
			}
			jsonResponse(rw, 201, src)
		default:
			rw.WriteHeader(405)
		}
	case "/api/v1/articles":
		switch r.Method {
		case "GET":
			sourceID, p := r.URL.Query().Get("source_id"), r.URL.Query().Get("path")
			if sourceID == "" && p == "" {
				jsonResponse(rw, 200, w.Store.snapshot(!owner && !reader).Pages)
				return
			}
			a, err := w.Store.article(sourceID, p, !owner && !reader)
			if err != nil {
				apiError(rw, err)
				return
			}
			if r.URL.Query().Get("history") != "1" || !owner {
				a.Versions = a.Versions[len(a.Versions)-1:]
				a.Conflicts = nil
			}
			if reader && !owner {
				a.Translation = nil
				a.CopiedFrom = nil
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
			if err := decodeBody(rw, r, &input); err != nil {
				apiError(rw, ErrInvalid)
				return
			}
			if input.Expected == nil || (r.Method == "POST" && *input.Expected != 0) || (r.Method == "PUT" && *input.Expected < 1) {
				apiError(rw, ErrInvalid)
				return
			}
			rev, err := w.Store.put(input.SourceID, input.Path, input.Text, *input.Expected)
			if err != nil {
				apiError(rw, err)
				return
			}
			if w.Sync != nil {
				w.Sync.Wake()
			}
			status := 200
			if r.Method == "POST" {
				status = 201
			}
			jsonResponse(rw, status, rev)
		default:
			rw.WriteHeader(405)
		}
	default:
		http.NotFound(rw, r)
	}
}
