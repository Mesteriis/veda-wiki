package main

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

func (w Wiki) githubAPI(rw http.ResponseWriter, r *http.Request, owner bool) {
	if !owner {
		http.NotFound(rw, r)
		return
	}
	if w.Sync == nil {
		jsonResponse(rw, 503, map[string]string{"error": "sync_unavailable"})
		return
	}
	switch r.URL.Path {
	case "/api/v1/github/publish-job":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in struct {
			ID       string `json:"job_id"`
			Expected int    `json:"expected_revision"`
			Confirm  bool   `json:"confirm_publication"`
		}
		if e := decodeBody(rw, r, &in); e != nil {
			apiError(rw, ErrInvalid)
			return
		}
		if e := w.Store.publishJob(in.ID, in.Expected, in.Confirm); e != nil {
			apiError(rw, e)
			return
		}
		w.Sync.Wake()
		jsonResponse(rw, 200, map[string]string{"state": "publication_approved"})
	case "/api/v1/github/accounts":
		if r.Method == "GET" {
			jsonResponse(rw, 200, w.Store.accounts())
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if r.TLS == nil && (ip == nil || !ip.IsLoopback()) {
			jsonResponse(rw, 403, map[string]string{"error": "credentials_require_https_or_loopback"})
			return
		}
		var in struct {
			ID string `json:"id"`
			GitHubSecret
		}
		if e := decodeBody(rw, r, &in); e != nil {
			apiError(rw, ErrInvalid)
			return
		}
		a, e := w.Store.saveAccount(r.Context(), w.Sync.github, in.ID, in.GitHubSecret)
		if e != nil {
			apiError(rw, e)
			return
		}
		jsonResponse(rw, 201, a)
	case "/api/v1/github/connections":
		if r.Method == "GET" {
			jsonResponse(rw, 200, w.Store.connections())
			return
		}
		if r.Method == "PUT" {
			var in configureRequest
			if e := decodeBody(rw, r, &in); e != nil {
				apiError(rw, ErrInvalid)
				return
			}
			c, e := w.Sync.configure(r.Context(), in)
			if e != nil {
				apiError(rw, e)
				return
			}
			jsonResponse(rw, 200, c)
			return
		}
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in connectRequest
		if e := decodeBody(rw, r, &in); e != nil {
			apiError(rw, ErrInvalid)
			return
		}
		c, e := w.Sync.connect(r.Context(), in)
		if e != nil {
			apiError(rw, e)
			return
		}
		jsonResponse(rw, 202, c)
	case "/api/v1/github/outbox":
		if r.Method != "GET" {
			rw.WriteHeader(405)
			return
		}
		jobs := w.Store.jobs()
		limit, offset := 30, 0
		query := r.URL.Query()
		if query.Has("limit") {
			n, e := strconv.Atoi(query.Get("limit"))
			if e != nil || n < 1 || n > 100 {
				apiError(rw, ErrInvalid)
				return
			}
			limit = n
		}
		if query.Has("offset") {
			n, e := strconv.Atoi(query.Get("offset"))
			if e != nil || n < 0 {
				apiError(rw, ErrInvalid)
				return
			}
			offset = n
		}
		start := min(offset, len(jobs))
		end := min(start+limit, len(jobs))
		jsonResponse(rw, 200, map[string]any{"jobs": jobs[start:end], "total": len(jobs), "offset": offset})
	case "/api/v1/github/sync", "/api/v1/github/pause":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in struct {
			ID     string `json:"id"`
			Paused bool   `json:"paused"`
		}
		if e := decodeBody(rw, r, &in); e != nil || !identifier.MatchString(in.ID) {
			apiError(rw, ErrInvalid)
			return
		}
		var e error
		if strings.HasSuffix(r.URL.Path, "/pause") {
			e = w.Store.pauseConnection(in.ID, in.Paused)
		} else {
			e = w.Store.retryConnection(in.ID)
		}
		if e != nil {
			apiError(rw, e)
			return
		}
		w.Sync.Wake()
		jsonResponse(rw, 202, map[string]string{"state": "scheduled"})
	default:
		http.NotFound(rw, r)
	}
}
