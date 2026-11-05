// Package httpapi serves the REST API, the browser sessions and the embedded
// reading interface. Handlers parse input, check the caller's role, call the
// area operations and serialize the result.
package httpapi

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
)

// Config describes one listener.
type Config struct {
	// Public serves only public content and ignores every credential.
	Public bool
	// ReaderLocal marks the private reader socket: requests without a key
	// read as a reader unless ReaderKeyRequired is set.
	ReaderLocal       bool
	ReaderKeyRequired bool
	// LocalImportSource is the private local source that root on the reader
	// socket may import into or remove; empty disables those routes.
	LocalImportSource string
	// AllowedHosts lists the accepted Host names; empty accepts any.
	AllowedHosts []string
	OwnerToken   string
	ReaderToken  string
}

// Deps are the services every listener of one process shares.
type Deps struct {
	Store *store.Store
	// Observations are the process's import observations. Every listener
	// must receive the same set; nil starts an empty one.
	Observations   *importer.Observations
	Sessions       *auth.Sessions
	AdminSessions  *auth.Sessions
	AccessSessions *auth.Sessions
	// Sync is the running synchronization manager, or nil when none runs.
	Sync *syncer.Manager
	// Knowledge is the knowledge service, or nil when the module is off.
	Knowledge *knowledge.Service
	// Activity records MCP clients; every listener of one process must
	// receive the same one. nil starts an empty one.
	Activity *ClientActivity
}

// LocalRootKey is the request context key, set only from kernel credentials
// on the reader socket, that marks a request from the local root user.
type LocalRootKey struct{}

// New returns the handler for one listener.
func New(cfg Config, deps Deps) http.Handler {
	h := handler{Config: cfg, Sessions: deps.Sessions, AdminSessions: deps.AdminSessions, AccessSessions: deps.AccessSessions, Sync: deps.Sync, Knowledge: deps.Knowledge, Activity: deps.Activity}
	if h.Activity == nil {
		h.Activity = &ClientActivity{}
	}
	if deps.Store != nil {
		observations := deps.Observations
		if observations == nil {
			observations = &importer.Observations{}
		}
		h.Store = &dataStore{Store: deps.Store, observations: observations}
	}
	return h
}

// handler is copied per request: role resolution may replace Store with a
// restricted reader's snapshot and mark an anonymous request Public.
type handler struct {
	Config
	Store          *dataStore
	Sessions       *auth.Sessions
	AdminSessions  *auth.Sessions
	AccessSessions *auth.Sessions
	Sync           *syncer.Manager
	Knowledge      *knowledge.Service
	Activity       *ClientActivity
}

func (h handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Referrer-Policy", "no-referrer")
	if h.Store != nil && h.Store.Invalid() {
		http.Error(rw, "Store unavailable", http.StatusServiceUnavailable)
		return
	}
	if !h.hostAllowed(r) {
		http.Error(rw, "Host not allowed", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/session/") || strings.HasPrefix(r.URL.Path, "/api/admin/session/") || strings.HasPrefix(r.URL.Path, "/api/access/session/") {
		h.sessionAPI(rw, r)
		return
	}
	if h.localRoute(rw, r) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/github/") {
		h.adminGitHubAPI(rw, r)
		return
	}
	if r.URL.Path == "/mcp" {
		h.remoteMCP(rw, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/mcp/") {
		h.adminMCPAPI(rw, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/access/keys") || r.URL.Path == "/api/access/sources" || r.URL.Path == "/api/access/users" || r.URL.Path == "/api/access/teams" {
		h.browserAccessAPI(rw, r)
		return
	}
	owner := false
	if h.Store != nil {
		var reader, ok bool
		owner, reader, ok = h.resolveRole(rw, r)
		if !ok {
			return
		}
		if mode := mcpMode(r, owner, reader); mode != "" {
			h.Activity.seen(mode, time.Now())
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			h.api(rw, r, owner, reader)
			return
		}
		if !owner && !reader {
			h.Public = true
		}
	}
	if strings.HasPrefix(r.URL.Path, "/media/") {
		h.media(rw, r, owner)
		return
	}
	rw.Header().Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; style-src 'self'; font-src 'self'; script-src 'self'; "+
		"img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if serveAssets(rw, r) {
		return
	}
	h.state(rw, r)
}

// hostAllowed accepts any Host when no allow-list is configured.
func (h handler) hostAllowed(r *http.Request) bool {
	if len(h.AllowedHosts) == 0 {
		return true
	}
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	for _, allowed := range h.AllowedHosts {
		if strings.EqualFold(host, allowed) {
			return true
		}
	}
	return false
}
