package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// knowledgeReindexBodyBytes bounds the reindex request body.
const knowledgeReindexBodyBytes = 4096

// knowledgeAPI serves /api/v1/knowledge/. Reads follow the computed mode
// (public := !owner && !reader) over the request-scoped store; status and
// reindex are owner-only and answer 404 to everyone else, like the other
// owner-only routes (a non-owner POST is refused earlier by the shared
// guard, and so is every browser POST). With the module off, status tells
// the owner so and every other route answers 503 knowledge_disabled.
// Errors are {"error":"<code>"} with fixed codes, as on other API routes; an unknown and an
// unreadable entity or article are the same 404 not_found.
func (h handler) knowledgeAPI(rw http.ResponseWriter, r *http.Request, owner, reader bool) {
	public := !owner && !reader
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/knowledge/")
	switch path {
	case "status", "reindex":
		if !owner {
			http.NotFound(rw, r)
			return
		}
	case "entities", "entity", "article", "graph", "context":
	default:
		http.NotFound(rw, r)
		return
	}
	method := "GET"
	if path == "reindex" {
		method = "POST"
	}
	if r.Method != method {
		knowledgeError(rw, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.Knowledge == nil {
		if path == "status" {
			jsonResponse(rw, 200, map[string]any{"enabled": false, "state": "disabled"})
			return
		}
		knowledgeError(rw, http.StatusServiceUnavailable, "knowledge_disabled")
		return
	}
	rs := knowledge.ReadScope{Store: h.Store.Store, Public: public, Owner: owner && !public, Now: time.Now()}
	ctx := r.Context()
	switch path {
	case "status":
		h.knowledgeStatus(rw, r)
	case "reindex":
		h.knowledgeReindex(rw, r)
	case "entities":
		q, ok := strictQuery(r, "q", "source_id", "type", "limit", "offset")
		limit, okLimit := intParam(q, "limit", 1)
		offset, okOffset := intParam(q, "offset", 0)
		if !ok || !okLimit || !okOffset {
			knowledgeError(rw, 400, "invalid_request")
			return
		}
		page, err := h.Knowledge.Entities(ctx, rs, knowledge.EntityQuery{Text: q.Get("q"), SourceID: q.Get("source_id"), Type: q.Get("type"), Limit: limit, Offset: offset})
		knowledgeAnswer(rw, page, err)
	case "entity":
		q, ok := strictQuery(r, "id", "limit")
		limit, okLimit := intParam(q, "limit", 1)
		if !ok || !okLimit {
			knowledgeError(rw, 400, "invalid_request")
			return
		}
		card, err := h.Knowledge.Entity(ctx, rs, q.Get("id"), limit)
		knowledgeAnswer(rw, card, err)
	case "article":
		q, ok := strictQuery(r, "source_id", "path", "sha256", "limit")
		limit, okLimit := intParam(q, "limit", 1)
		if !ok || !okLimit {
			knowledgeError(rw, 400, "invalid_request")
			return
		}
		ref := knowledge.ArticleRef{SourceID: q.Get("source_id"), Path: q.Get("path")}
		view, err := h.Knowledge.Article(ctx, rs, ref, q.Get("sha256"), limit)
		knowledgeAnswer(rw, view, err)
	case "graph":
		req, ok := graphRequest(r)
		if !ok {
			knowledgeError(rw, 400, "invalid_request")
			return
		}
		g, err := h.Knowledge.Graph(ctx, rs, req)
		knowledgeAnswer(rw, g, err)
	case "context":
		h.knowledgeContext(rw, r, rs, public)
	}
}

func (h handler) knowledgeContext(rw http.ResponseWriter, r *http.Request, rs knowledge.ReadScope, public bool) {
	q, ok := strictQuery(r, "q", "depth", "limit_bytes")
	depth, okDepth := intParam(q, "depth", 1)
	limitBytes, okBytes := intParam(q, "limit_bytes", 1)
	text := strings.TrimSpace(q.Get("q"))
	if !ok || !okDepth || !okBytes || text == "" {
		knowledgeError(rw, 400, "invalid_request")
		return
	}
	var seeds []knowledge.ArticleRef
	if len([]rune(text)) <= knowledge.MaxQueryRunes {
		for _, hit := range h.Store.findArticles(text, "", public) {
			if len(seeds) == knowledge.MaxContextSeeds {
				break
			}
			seeds = append(seeds, knowledge.ArticleRef{SourceID: hit.SourceID, Path: hit.Path})
		}
	}
	answer, err := h.Knowledge.Context(r.Context(), rs, knowledge.ContextRequest{Text: text, Seeds: seeds, Depth: depth, LimitBytes: limitBytes})
	knowledgeAnswer(rw, answer, err)
}

func graphRequest(r *http.Request) (knowledge.GraphRequest, bool) {
	q, ok := strictQuery(r, "entity_id", "source_id", "path", "sha256", "depth", "sources", "relations", "max_nodes", "max_edges")
	depth, okDepth := intParam(q, "depth", 1)
	maxNodes, okNodes := intParam(q, "max_nodes", 1)
	maxEdges, okEdges := intParam(q, "max_edges", 1)
	if !ok || !okDepth || !okNodes || !okEdges {
		return knowledge.GraphRequest{}, false
	}
	req := knowledge.GraphRequest{EntityID: q.Get("entity_id"), SHA: q.Get("sha256"), Depth: depth, MaxNodes: maxNodes, MaxEdges: maxEdges}
	if q.Has("source_id") || q.Has("path") {
		req.Article = &knowledge.ArticleRef{SourceID: q.Get("source_id"), Path: q.Get("path")}
	}
	var listOK bool
	if req.SourceIDs, listOK = listParam(q, "sources"); !listOK {
		return knowledge.GraphRequest{}, false
	}
	if req.Relations, listOK = listParam(q, "relations"); !listOK {
		return knowledge.GraphRequest{}, false
	}
	return req, true
}

func (h handler) knowledgeStatus(rw http.ResponseWriter, r *http.Request) {
	if _, ok := strictQuery(r); !ok {
		knowledgeError(rw, 400, "invalid_request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Knowledge.ReadTimeout())
	defer cancel()
	jsonResponse(rw, 200, h.Knowledge.Status(ctx))
}

// reindexRequest is the strict body of POST /api/v1/knowledge/reindex.
type reindexRequest struct {
	Scope    string `json:"scope"`
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Force    bool   `json:"force"`
}

// knowledgeReindex queues an owner reindex of everything, a source or an
// article and answers 202 with the operation; a running one is 409.
func (h handler) knowledgeReindex(rw http.ResponseWriter, r *http.Request) {
	var in reindexRequest
	if _, ok := strictQuery(r); !ok || decodeLimited(rw, r, &in, knowledgeReindexBodyBytes) != nil {
		knowledgeError(rw, 400, "invalid_request")
		return
	}
	valid := false
	switch in.Scope {
	case "all":
		valid = in.SourceID == "" && in.Path == ""
	case "source":
		valid = domain.ValidID(in.SourceID) && in.Path == ""
	case "article":
		valid = domain.ValidID(in.SourceID) && domain.SafeArticlePath(in.Path)
	}
	if !valid {
		knowledgeError(rw, 400, "invalid_request")
		return
	}
	if in.Scope != "all" && !h.knowledgeTargetExists(in.SourceID, in.Path) {
		knowledgeError(rw, 404, "not_found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Knowledge.ReadTimeout())
	defer cancel()
	op, err := h.Knowledge.Reindex(ctx, knowledge.ReindexScope{SourceID: in.SourceID, Path: in.Path, Force: in.Force})
	switch {
	case errors.Is(err, knowledge.ErrReindexBusy):
		knowledgeError(rw, 409, "reindex_busy")
	case err != nil:
		knowledgeError(rw, 503, "knowledge_unavailable")
	default:
		jsonResponse(rw, 202, op)
	}
}

// knowledgeTargetExists reports a source, or an article that is not
// deleted, in the owner's store.
func (h handler) knowledgeTargetExists(sourceID, p string) bool {
	found := false
	_ = h.Store.View(func(db *domain.Database) error {
		if _, ok := db.Sources[sourceID]; !ok {
			return nil
		}
		if p == "" {
			found = true
			return nil
		}
		a, ok := db.Articles[sourceID+"/"+p]
		found = ok && !a.Deleted && a.SourceID == sourceID && a.Path == p
		return nil
	})
	return found
}

// knowledgeAnswer writes a read result or its fixed error code.
func knowledgeAnswer(rw http.ResponseWriter, v any, err error) {
	switch {
	case err == nil:
		jsonResponse(rw, 200, v)
	case errors.Is(err, knowledge.ErrInvalidRequest):
		knowledgeError(rw, 400, "invalid_request")
	case errors.Is(err, knowledge.ErrNotFound):
		knowledgeError(rw, 404, "not_found")
	case errors.Is(err, context.DeadlineExceeded):
		knowledgeError(rw, 504, "timeout")
	default:
		knowledgeError(rw, 503, "knowledge_unavailable")
	}
}

func knowledgeError(rw http.ResponseWriter, status int, code string) {
	jsonResponse(rw, status, map[string]string{"error": code})
}

// strictQuery parses the query and refuses unknown or repeated parameters.
func strictQuery(r *http.Request, names ...string) (url.Values, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, false
	}
	for name, values := range q {
		known := false
		for _, n := range names {
			known = known || n == name
		}
		if !known || len(values) != 1 {
			return nil, false
		}
	}
	return q, true
}

// intParam reads an optional decimal parameter of at least lowest; absent
// is 0. Upper bounds are checked by the read service.
func intParam(q url.Values, name string, lowest int) (int, bool) {
	if q == nil || !q.Has(name) {
		return 0, true
	}
	raw := q.Get(name)
	n, err := strconv.Atoi(raw)
	if err != nil || n < lowest || raw != strconv.Itoa(n) || len(raw) > 6 {
		return 0, false
	}
	return n, true
}

// listParam reads an optional comma-separated list without empty items.
func listParam(q url.Values, name string) ([]string, bool) {
	if !q.Has(name) {
		return nil, true
	}
	items := strings.Split(q.Get(name), ",")
	for _, item := range items {
		if item == "" {
			return nil, false
		}
	}
	return items, true
}
