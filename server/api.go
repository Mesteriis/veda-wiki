package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
func jsonResponse(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.WriteHeader(status)
	json.NewEncoder(rw).Encode(v)
}
func decodeBody(rw http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 300000))
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
func (w Wiki) api(rw http.ResponseWriter, r *http.Request, owner bool) {
	if r.Method != "GET" && (!owner || r.Header.Get("Origin") != "") {
		jsonResponse(rw, 403, map[string]string{"error": "owner authorization required; browser writes are disabled"})
		return
	}
	switch r.URL.Path {
	case "/api/v1/sources":
		switch r.Method {
		case "GET":
			jsonResponse(rw, 200, w.Store.sources(!owner))
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
				jsonResponse(rw, 200, w.Store.snapshot(!owner).Pages)
				return
			}
			a, err := w.Store.article(sourceID, p, !owner)
			if err != nil {
				apiError(rw, err)
				return
			}
			if r.URL.Query().Get("history") != "1" || !owner {
				a.Versions = a.Versions[len(a.Versions)-1:]
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
