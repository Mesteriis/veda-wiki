package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

// authorized reports an owner bearer token. The public listener never
// accepts one.
func (h handler) authorized(r *http.Request) bool {
	if h.Public || h.OwnerToken == "" {
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.OwnerToken)) == 1
}

// A reader credential grants current content only. It never grants writes or
// administrative APIs, even when presented to the private listener.
func (h handler) readerAuthorized(r *http.Request) bool {
	if h.Public || h.ReaderToken == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.ReaderToken)) == 1
}

// validationRequest reports the only POST route a reader may call.
func validationRequest(r *http.Request) bool {
	return r.Method == "POST" && r.URL.Path == "/api/v1/articles/transfer-plan"
}

// resolveRole decides whether the request reads as the owner or as a reader.
// A scoped key, a scoped reader session or a team user session replaces
// h.Store with the snapshot of the sources it grants. The public listener
// ignores every credential. It returns false after answering the request.
func (h *handler) resolveRole(rw http.ResponseWriter, r *http.Request) (owner, reader, ok bool) {
	owner = h.authorized(r)
	implicitLocal := h.ReaderLocal && !h.ReaderKeyRequired && r.Header.Get("Authorization") == ""
	validation := validationRequest(r) && r.Header.Get("Origin") == ""
	reader = (h.readerAuthorized(r) || (!h.Public && (implicitLocal || (!validation && h.Sessions.Valid(r))))) && (r.Method == "GET" || validation)
	if !h.Public && !owner && !reader && r.Method == "GET" {
		if hash, found := h.Sessions.ScopedCookie(r); found {
			scoped, valid, err := h.Store.auth().ScopedViewHash(hash, time.Now())
			if err != nil {
				http.Error(rw, "Access unavailable", http.StatusServiceUnavailable)
				return false, false, false
			}
			if valid {
				h.Store, reader = h.Store.scoped(scoped), true
			}
		}
		if user, found := h.Sessions.UserCookie(r); found && !reader {
			scoped, valid, err := h.Store.auth().ScopedViewUser(user.ID, user.AuthVersion)
			if err != nil {
				http.Error(rw, "Access unavailable", http.StatusServiceUnavailable)
				return false, false, false
			}
			if valid {
				h.Store, reader = h.Store.scoped(scoped), true
			}
		}
	}
	if !h.Public && !owner && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		scoped, valid, err := h.Store.auth().ScopedView(token, time.Now())
		if err != nil {
			http.Error(rw, "Access unavailable", http.StatusServiceUnavailable)
			return false, false, false
		}
		if valid {
			if r.Method != "GET" && !validation {
				http.Error(rw, "Read-only access", http.StatusForbidden)
				return false, false, false
			}
			h.Store, reader = h.Store.scoped(scoped), true
		}
	}
	return owner, reader, true
}

// api is the shared guard: only the owner writes, and never from a browser.
func (h handler) api(rw http.ResponseWriter, r *http.Request, owner, reader bool) {
	validation := validationRequest(r) && reader
	if r.Method != "GET" && ((!owner && !validation) || r.Header.Get("Origin") != "") {
		jsonResponse(rw, 403, map[string]string{"error": "owner authorization required; browser writes are disabled"})
		return
	}
	h.routeAPI(rw, r, owner, reader)
}

// routeAPI checks the required role in every route, independently of the
// shared guard in api(). Non-owners may only read and run the two
// validations; owner-only routes answer 404 to everyone else, and routes
// open to readers but not the public answer 404 to anonymous callers.
func (h handler) routeAPI(rw http.ResponseWriter, r *http.Request, owner, reader bool) {
	if r.Method != "GET" && !owner && !(validationRequest(r) && reader) {
		jsonResponse(rw, 403, map[string]string{"error": "owner authorization required"})
		return
	}
	public := !owner && !reader
	path := r.URL.Path
	switch {
	case path == "/api/v1/access-keys" || path == "/api/v1/access-keys/revoke":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		h.accessKeys(rw, r)
		return
	case path == "/api/v1/users" || path == "/api/v1/teams":
		if !owner {
			http.NotFound(rw, r)
			return
		}
		h.people(rw, r)
		return
	case strings.HasPrefix(path, "/api/v1/mcp/"):
		if !owner {
			http.NotFound(rw, r)
			return
		}
		h.mcpAPI(rw, r)
		return
	case strings.HasPrefix(path, "/api/v1/knowledge/"):
		h.knowledgeAPI(rw, r, owner, reader)
		return
	case strings.HasPrefix(path, "/api/v1/github/"):
		if !owner {
			http.NotFound(rw, r)
			return
		}
		h.githubAPI(rw, r)
		return
	}
	switch path {
	case "/api/v1/articles/new-proposal-base", "/api/v1/articles/proposal-base":
		if public {
			http.NotFound(rw, r)
			return
		}
	case "/api/v1/moves", "/api/v1/moves/cancel", "/api/v1/sources/mirrors", "/api/v1/conflicts", "/api/v1/conflicts/resolve", "/api/v1/import":
		if !owner {
			http.NotFound(rw, r)
			return
		}
	}
	switch path {
	case "/api/v1/articles/new-proposal-base":
		h.proposalBase(rw, r, true)
	case "/api/v1/articles/proposal-base":
		h.proposalBase(rw, r, false)
	case "/api/v1/articles/excerpt":
		h.excerpt(rw, r, public)
	case "/api/v1/context":
		h.context(rw, r, public)
	case "/api/v1/search":
		h.search(rw, r, public)
	case "/api/v1/moves/cancel":
		h.cancelMove(rw, r)
	case "/api/v1/moves":
		h.moves(rw, r)
	case "/api/v1/articles/transfer-plan":
		h.transferPlan(rw, r)
	case "/api/v1/sources/mirrors":
		h.mirrors(rw, r)
	case "/api/v1/sources/reconcile":
		h.reconcile(rw, r)
	case "/api/v1/articles/copy":
		h.copyArticle(rw, r)
	case "/api/v1/conflicts":
		h.conflicts(rw, r)
	case "/api/v1/conflicts/resolve":
		h.resolveConflict(rw, r)
	case "/api/v1/import":
		h.importBatch(rw, r)
	case "/api/v1/sources":
		h.sources(rw, r, owner, reader)
	case "/api/v1/articles":
		h.articles(rw, r, owner, reader)
	default:
		http.NotFound(rw, r)
	}
}
