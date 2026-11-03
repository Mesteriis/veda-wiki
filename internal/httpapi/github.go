package httpapi

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
)

// Browser administration is restricted to GitHub account and connection
// management. The cookie cannot authorize the owner API or article writes.
func (h handler) adminGitHubAPI(rw http.ResponseWriter, r *http.Request) {
	if h.Public || h.Store == nil || !h.adminCookieValid(r, h.AdminSessions, auth.AdminCookie) {
		http.NotFound(rw, r)
		return
	}
	if r.URL.Path != "/api/admin/github/accounts" && r.URL.Path != "/api/admin/github/connections" && r.URL.Path != "/api/admin/github/sync" && r.URL.Path != "/api/admin/github/pause" && r.URL.Path != "/api/admin/github/mirrors" {
		http.NotFound(rw, r)
		return
	}
	if r.Method != "GET" && r.Method != "POST" && !(r.Method == "PUT" && r.URL.Path == "/api/admin/github/connections") {
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method != "GET" && !auth.SameOrigin(r) {
		jsonResponse(rw, http.StatusForbidden, map[string]string{"error": "origin rejected"})
		return
	}
	request := r.Clone(r.Context())
	if r.URL.Path == "/api/admin/github/mirrors" {
		request.URL.Path = "/api/v1/sources/mirrors"
		request.Header = r.Header.Clone()
		request.Header.Del("Origin") // same-origin was checked above for writes
		h.api(rw, request, true, false)
		return
	}
	request.URL.Path = strings.Replace(r.URL.Path, "/api/admin/github/", "/api/v1/github/", 1)
	h.githubAPI(rw, request)
}

// githubAPI manages GitHub accounts, connections and the outbox. The caller
// has checked that the request comes from the owner.
func (h handler) githubAPI(rw http.ResponseWriter, r *http.Request) {
	if h.Sync == nil {
		jsonResponse(rw, 503, map[string]string{"error": "sync_unavailable"})
		return
	}
	switch r.URL.Path {
	case "/api/v1/github/cancel-bundle":
		if r.Method != "POST" {
			rw.WriteHeader(405)
			return
		}
		var in struct {
			ID      string `json:"job_id"`
			Confirm bool   `json:"confirm_cancel"`
		}
		if decodeBody(rw, r, &in) != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		if err := h.Sync.CancelBundle(in.ID, in.Confirm); err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, map[string]string{"state": "superseded"})
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
			apiError(rw, domain.ErrInvalid)
			return
		}
		if e := h.Sync.PublishJob(in.ID, in.Expected, in.Confirm); e != nil {
			apiError(rw, e)
			return
		}
		h.Sync.Wake()
		jsonResponse(rw, 200, map[string]string{"state": "publication_approved"})
	case "/api/v1/github/accounts":
		if r.Method == "GET" {
			jsonResponse(rw, 200, h.Sync.Accounts())
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
			github.Secret
		}
		if e := decodeBody(rw, r, &in); e != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		a, e := h.Sync.SaveAccount(r.Context(), in.ID, in.Secret)
		if e != nil {
			apiError(rw, e)
			return
		}
		jsonResponse(rw, 201, a)
	case "/api/v1/github/connections":
		if r.Method == "GET" {
			jsonResponse(rw, 200, h.Sync.Connections())
			return
		}
		if r.Method == "PUT" {
			var in syncer.ConfigureRequest
			if e := decodeBody(rw, r, &in); e != nil {
				apiError(rw, domain.ErrInvalid)
				return
			}
			c, e := h.Sync.Configure(r.Context(), in)
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
		var in syncer.ConnectRequest
		if e := decodeBody(rw, r, &in); e != nil {
			apiError(rw, domain.ErrInvalid)
			return
		}
		if in.AdoptExisting {
			// Adoption downloads and verifies a complete tree before committing.
			// Give that request a bounded window without changing other routes.
			_ = http.NewResponseController(rw).SetWriteDeadline(time.Now().Add(6 * time.Minute))
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
			defer cancel()
			r = r.WithContext(ctx)
		}
		c, e := h.Sync.Connect(r.Context(), in)
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
		jobs := h.Sync.Jobs()
		limit, offset := 30, 0
		query := r.URL.Query()
		if query.Has("limit") {
			n, e := strconv.Atoi(query.Get("limit"))
			if e != nil || n < 1 || n > 100 {
				apiError(rw, domain.ErrInvalid)
				return
			}
			limit = n
		}
		if query.Has("offset") {
			n, e := strconv.Atoi(query.Get("offset"))
			if e != nil || n < 0 {
				apiError(rw, domain.ErrInvalid)
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
			Paused *bool  `json:"paused"`
		}
		if e := decodeBody(rw, r, &in); e != nil || !domain.IDPattern.MatchString(in.ID) {
			apiError(rw, domain.ErrInvalid)
			return
		}
		var e error
		if strings.HasSuffix(r.URL.Path, "/pause") {
			if in.Paused == nil {
				apiError(rw, domain.ErrInvalid)
				return
			}
			e = h.Sync.PauseConnection(in.ID, *in.Paused)
		} else {
			e = h.Sync.RetryConnection(in.ID)
		}
		if e != nil {
			apiError(rw, e)
			return
		}
		h.Sync.Wake()
		jsonResponse(rw, 202, map[string]string{"state": "scheduled"})
	default:
		http.NotFound(rw, r)
	}
}
