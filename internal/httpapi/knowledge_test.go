package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

var kgReadRoutes = []string{
	"/api/v1/knowledge/entities", "/api/v1/knowledge/entity?id=ent-00000000000000000000",
	"/api/v1/knowledge/article?source_id=pub&path=billing.md", "/api/v1/knowledge/graph?source_id=pub&path=billing.md",
	"/api/v1/knowledge/context?q=billing",
}

func kgCall(w Wiki, method, path, body, bearer, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	out := httptest.NewRecorder()
	w.ServeHTTP(out, r)
	return out
}

// §12-1 (REST side): without the module every knowledge read answers 503
// knowledge_disabled, status tells only the owner, reindex is refused, and
// /api/state carries no knowledge data either way.
func TestKnowledgeDisabled(t *testing.T) {
	s := testStore(t)
	source(t, s, "pub", "public")
	w := Wiki{Store: s, Token: "test-owner", ReaderToken: "test-reader"}
	for _, p := range kgReadRoutes {
		for _, bearer := range []string{"", "test-owner", "test-reader"} {
			out := kgGet(w, p, bearer)
			if out.Code != 503 || kgErrorCode(t, out) != "knowledge_disabled" {
				t.Fatalf("%s (%q): %d %s", p, bearer, out.Code, out.Body.String())
			}
		}
	}
	if out := kgGet(w, "/api/v1/knowledge/status", "test-owner"); out.Code != 200 || out.Body.String() != "{\"enabled\":false,\"state\":\"disabled\"}\n" {
		t.Fatalf("owner status: %d %s", out.Code, out.Body.String())
	}
	if out := kgGet(w, "/api/v1/knowledge/status", "test-reader"); out.Code != 404 {
		t.Fatalf("reader status: %d", out.Code)
	}
	if out := kgCall(w, "POST", "/api/v1/knowledge/reindex", `{"scope":"all"}`, "test-owner", ""); out.Code != 503 || kgErrorCode(t, out) != "knowledge_disabled" {
		t.Fatalf("reindex: %d %s", out.Code, out.Body.String())
	}
	h := newKGHarness(t)
	h.seed()
	h.start()
	h.settle()
	for _, w := range []Wiki{w, h.wiki()} {
		if body := kgGet(w, "/api/state", "test-owner").Body.String(); strings.Contains(body, "knowledge") || strings.Contains(body, "entities") {
			t.Fatalf("/api/state carries knowledge data: %s", body)
		}
	}
}

// Unknown knowledge routes answer exactly like an unknown API route.
func TestKnowledgeUnknownRoutes(t *testing.T) {
	h := newKGHarness(t)
	h.seed()
	want := kgGet(h.wiki(), "/api/v1/never-existed", "test-owner")
	for _, p := range []string{"/api/v1/knowledge/", "/api/v1/knowledge/cypher", "/api/v1/knowledge/query", "/api/v1/knowledge/entities/x"} {
		for _, w := range []Wiki{h.wiki(), h.public()} {
			if got := kgGet(w, p, "test-owner"); got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Fatalf("%s: %d %q", p, got.Code, got.Body.String())
			}
		}
	}
}

// §12-11 (REST side) and §12-9: status and reindex are owner-only. The
// public listener ignores the owner token, the reader socket and reader
// keys cannot reach them, and a browser POST is refused even with the
// owner's token or an administrator session.
func TestKnowledgeOwnerRoutesByMode(t *testing.T) {
	h := newKGHarness(t)
	h.seed()
	h.start()
	h.settle()
	socket := Wiki{Store: h.s, ReaderLocal: true, Knowledge: h.svc}
	keyed := socket
	keyed.ReaderKeyRequired = true
	keyed.ReaderToken = "test-reader"
	cases := []struct {
		name           string
		w              Wiki
		bearer, origin string
		status, post   int
	}{
		{"owner", h.wiki(), "test-owner", "", 200, 202},
		{"owner from a browser", h.wiki(), "test-owner", "http://localhost:8080", 200, 403},
		{"public listener with owner token", h.public(), "test-owner", "", 404, 403},
		{"reader key", h.wiki(), "test-reader", "", 404, 403},
		{"scoped key", h.wiki(), h.scoped, "", 404, 403},
		{"anonymous", h.wiki(), "", "", 404, 403},
		{"reader socket", socket, "", "", 404, 403},
		{"reader socket with key", keyed, "test-reader", "", 404, 403},
	}
	for _, c := range cases {
		if got := kgCall(c.w, "GET", "/api/v1/knowledge/status", "", c.bearer, c.origin).Code; got != c.status {
			t.Errorf("%s: status %d, want %d", c.name, got, c.status)
		}
		if got := kgCall(c.w, "POST", "/api/v1/knowledge/reindex", `{"scope":"all"}`, c.bearer, c.origin).Code; got != c.post {
			t.Errorf("%s: reindex %d, want %d", c.name, got, c.post)
		}
		if c.name != "owner" && c.name != "owner from a browser" {
			if got := kgCall(c.w, "GET", "/api/v1/knowledge/entities", "", c.bearer, c.origin).Code; got != 200 {
				t.Errorf("%s: reads refused: %d", c.name, got)
			}
		}
	}
	// A browser administrator session is not the owner and cannot reindex.
	admin := &auth.Sessions{}
	cookie, err := admin.Issue(auth.Login{Now: time.Now(), Authenticate: func() (auth.Grant, bool) { return auth.Grant{}, true }})
	if err != nil {
		t.Fatal(err)
	}
	w := h.wiki()
	w.AdminSessions, w.Sessions = admin, admin
	for _, name := range []string{auth.AdminCookie, auth.ReaderCookie} {
		r := httptest.NewRequest("POST", "http://localhost:8080/api/v1/knowledge/reindex", strings.NewReader(`{"scope":"all"}`))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://localhost:8080")
		r.AddCookie(&http.Cookie{Name: name, Value: cookie})
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		if out.Code != 403 {
			t.Fatalf("browser session %s reindexed: %d", name, out.Code)
		}
	}
	// The owner's status carries the backlog; nobody else gets it.
	st := kgDecode[knowledge.Status](t, kgGet(h.wiki(), "/api/v1/knowledge/status", "test-owner"))
	if !st.Enabled || st.Fingerprint == "" || st.Jobs == nil {
		t.Fatalf("owner status: %+v", st)
	}
	if body := kgGet(h.wiki(), "/api/v1/knowledge/article?source_id=alpha&path=guide.md", "test-reader").Body.String(); strings.Contains(body, "jobs") || strings.Contains(body, "pending") {
		t.Fatalf("reader article answer carries backlog data: %s", body)
	}
}

func TestKnowledgeReindex(t *testing.T) {
	h := newKGHarness(t)
	h.seed()
	h.start()
	h.settle()
	post := func(body string) *httptest.ResponseRecorder {
		return kgCall(h.wiki(), "POST", "/api/v1/knowledge/reindex", body, "test-owner", "")
	}
	for _, body := range []string{`{"scope":"all"}`, `{"scope":"source","source_id":"pub"}`, `{"scope":"article","source_id":"pub","path":"billing.md","force":true}`} {
		out := post(body)
		op := struct {
			ID string `json:"operation_id"`
		}{}
		if out.Code != 202 || kgDecodeInto(t, out, &op) || !strings.HasPrefix(op.ID, "reindex-") {
			t.Fatalf("%s: %d %s", body, out.Code, out.Body.String())
		}
		h.settle()
	}
	for body, code := range map[string]string{
		`{"scope":"all","source_id":"pub"}`:                    "invalid_request",
		`{"scope":"source"}`:                                   "invalid_request",
		`{"scope":"source","source_id":"pub","path":"a.md"}`:   "invalid_request",
		`{"scope":"article","source_id":"pub"}`:                "invalid_request",
		`{"scope":"everything"}`:                               "invalid_request",
		`{"scope":"all","cypher":"MATCH (n) DETACH DELETE n"}`: "invalid_request",
		`{"scope":"all"} {"scope":"all"}`:                      "invalid_request",
		`{"scope":"source","source_id":"missing"}`:             "not_found",
		`{"scope":"article","source_id":"pub","path":"no.md"}`: "not_found",
	} {
		if out := post(body); kgErrorCode(t, out) != code {
			t.Errorf("%s: %d %s", body, out.Code, out.Body.String())
		}
	}
	if out := post(strings.Repeat(" ", 5000) + `{"scope":"all"}`); out.Code != 400 {
		t.Errorf("oversized body: %d", out.Code)
	}
	// One operation at a time.
	gate := make(chan struct{})
	h.manifest.gate.Store(&gate)
	first := make(chan *httptest.ResponseRecorder)
	go func() { first <- post(`{"scope":"all"}`) }()
	<-h.manifest.entered
	if out := post(`{"scope":"all"}`); out.Code != 409 || kgErrorCode(t, out) != "reindex_busy" {
		t.Fatalf("concurrent reindex: %d %s", out.Code, out.Body.String())
	}
	h.manifest.gate.Store(nil)
	close(gate)
	if out := <-first; out.Code != 202 {
		t.Fatalf("held reindex: %d", out.Code)
	}
	if out := kgCall(h.wiki(), "GET", "/api/v1/knowledge/reindex", "", "test-owner", ""); out.Code != 405 {
		t.Fatalf("GET reindex: %d", out.Code)
	}
}

func kgDecodeInto(t *testing.T, out *httptest.ResponseRecorder, v any) bool {
	t.Helper()
	return json.Unmarshal(out.Body.Bytes(), v) != nil
}

// Strict, bounded parameters; caps set truncated; entities page.
func TestKnowledgeLimitsAndPagination(t *testing.T) {
	h := newKGHarness(t)
	h.seed()
	h.start()
	h.settle()
	w := h.wiki()
	invalid := []string{
		"/api/v1/knowledge/entities?limit=101", "/api/v1/knowledge/entities?limit=0", "/api/v1/knowledge/entities?offset=-1",
		"/api/v1/knowledge/entities?offset=10001", "/api/v1/knowledge/entities?limit=01", "/api/v1/knowledge/entities?q=a&q=b",
		"/api/v1/knowledge/entities?unknown=1", "/api/v1/knowledge/entities?type=planet", "/api/v1/knowledge/entities?source_id=..%2Fx",
		"/api/v1/knowledge/entities?q=" + strings.Repeat("x", 201), "/api/v1/knowledge/entities?cypher=MATCH",
		"/api/v1/knowledge/entity", "/api/v1/knowledge/entity?id=ent-zz", "/api/v1/knowledge/entity?id=ent-00000000000000000000&limit=101",
		"/api/v1/knowledge/article?source_id=pub", "/api/v1/knowledge/article?source_id=pub&path=..%2Fx.md", "/api/v1/knowledge/article?source_id=pub&path=billing.md&sha256=abc",
		"/api/v1/knowledge/graph", "/api/v1/knowledge/graph?entity_id=ent-00000000000000000000&source_id=pub&path=billing.md",
		"/api/v1/knowledge/graph?source_id=pub&path=billing.md&depth=3", "/api/v1/knowledge/graph?source_id=pub&path=billing.md&depth=0",
		"/api/v1/knowledge/graph?source_id=pub&path=billing.md&max_nodes=201", "/api/v1/knowledge/graph?source_id=pub&path=billing.md&max_edges=401",
		"/api/v1/knowledge/graph?source_id=pub&path=billing.md&relations=knows", "/api/v1/knowledge/graph?source_id=pub&path=billing.md&sources=a,,b",
		"/api/v1/knowledge/graph?source_id=pub&path=billing.md&sources=" + strings.Repeat("a,", 20) + "a",
		"/api/v1/knowledge/context", "/api/v1/knowledge/context?q=%20", "/api/v1/knowledge/context?q=x&limit_bytes=16001",
		"/api/v1/knowledge/context?q=x&depth=3", "/api/v1/knowledge/context?q=" + strings.Repeat("x", 201),
		"/api/v1/knowledge/status?verbose=1",
	}
	for _, p := range invalid {
		if out := kgGet(w, p, "test-owner"); out.Code != 400 || kgErrorCode(t, out) != "invalid_request" {
			t.Errorf("%s: %d %s", p, out.Code, out.Body.String())
		}
	}
	all := kgDecode[knowledge.EntityPage](t, kgGet(w, "/api/v1/knowledge/entities", "test-owner"))
	first := kgDecode[knowledge.EntityPage](t, kgGet(w, "/api/v1/knowledge/entities?limit=1", "test-owner"))
	second := kgDecode[knowledge.EntityPage](t, kgGet(w, "/api/v1/knowledge/entities?limit=1&offset=1", "test-owner"))
	if all.Total < 3 || len(first.Entities) != 1 || first.Next != 1 || first.Total != all.Total || second.Entities[0].ID != all.Entities[1].ID {
		t.Fatalf("pagination: all %d, first %+v, second %+v", all.Total, first, second)
	}
	g := kgDecode[knowledge.Subgraph](t, kgGet(w, "/api/v1/knowledge/graph?source_id=pub&path=billing.md&max_nodes=2&max_edges=1", "test-owner"))
	if !g.Truncated || len(g.Nodes) > 2 || len(g.Edges) > 1 {
		t.Fatalf("graph caps: %+v", g)
	}
	full := kgDecode[knowledge.Subgraph](t, kgGet(w, "/api/v1/knowledge/graph?source_id=pub&path=billing.md&depth=2&relations=uses,runs_on&sources=pub", "test-owner"))
	if full.Truncated || len(full.Nodes) < 3 || len(full.Nodes) > 200 || len(full.Edges) > 400 {
		t.Fatalf("graph: %+v", full)
	}
	c := kgDecode[knowledge.ContextAnswer](t, kgGet(w, "/api/v1/knowledge/context?q=billing+db1+Redis&limit_bytes=600", "test-owner"))
	if c.LimitBytes != 600 || c.Bytes > 600 || !c.Truncated || len(c.Items) == 0 {
		t.Fatalf("context cap: %+v", c)
	}
	c = kgDecode[knowledge.ContextAnswer](t, kgGet(w, "/api/v1/knowledge/context?q=billing", "test-owner"))
	reasons := map[string]bool{}
	for _, item := range c.Items {
		reasons[item.Reason] = true
	}
	if c.LimitBytes != knowledge.DefaultContextLimitBytes || c.Depth != 1 || !reasons[knowledge.ReasonEntityMatch] || !reasons[knowledge.ReasonLexicalHit] {
		t.Fatalf("context reasons: %+v", c)
	}
	if out := kgCall(w, "POST", "/api/v1/knowledge/entities", "", "test-owner", ""); out.Code != 405 {
		t.Fatalf("POST read route: %d", out.Code)
	}
}

var errFakeOutage = errors.New("synthetic outage")

// Index queries have a deadline; the error code carries no internals.
func TestKnowledgeReadTimeoutAndOutage(t *testing.T) {
	h := newKGHarnessWith(t, 30*time.Millisecond)
	h.seed()
	h.start()
	h.settle()
	h.ix.slow.Store(true)
	if out := kgGet(h.wiki(), "/api/v1/knowledge/entities", "test-owner"); out.Code != 504 || kgErrorCode(t, out) != "timeout" {
		t.Fatalf("slow index: %d %s", out.Code, out.Body.String())
	}
	h.ix.slow.Store(false)
	h.mem.SetUnavailable(errFakeOutage)
	for _, p := range []string{"/api/v1/knowledge/entities", "/api/v1/knowledge/graph?source_id=pub&path=billing.md", "/api/v1/knowledge/context?q=billing"} {
		out := kgGet(h.wiki(), p, "test-owner")
		if out.Code != 503 || kgErrorCode(t, out) != "knowledge_unavailable" || strings.Contains(out.Body.String(), "outage") {
			t.Fatalf("%s during an index outage: %d %s", p, out.Code, out.Body.String())
		}
	}
	a := kgDecode[knowledge.ArticleKnowledge](t, kgGet(h.wiki(), "/api/v1/knowledge/article?source_id=pub&path=billing.md", "test-reader"))
	if a.Status != knowledge.ArticleUnavailable || a.Knowledge != nil {
		t.Fatalf("article during an index outage: %+v", a)
	}
	// Ordinary reading is unaffected.
	if out := kgGet(h.wiki(), "/api/v1/articles?source_id=pub&path=billing.md", "test-reader"); out.Code != 200 {
		t.Fatalf("article read during a knowledge outage: %d", out.Code)
	}
}

// Article statuses: ready, failed (detail for the owner only), too_large,
// and processing / unavailable before the pipeline is known.
func TestKnowledgeArticleStatuses(t *testing.T) {
	h := newKGHarness(t)
	h.seed()
	h.put("pub", "rejected.md", "# Rejected\n\nREJECT this text.\n")
	h.s.mutate(t, func(db *domain.Database) {
		text := "# Large\n\n" + strings.Repeat("billing ", 140000)
		db.Articles["pub/large.md"] = domain.Article{SourceID: "pub", Path: "large.md", Versions: []domain.Revision{{Number: 1, Text: text, SHA: domain.Digest([]byte(text)), Created: "2026-11-04T00:00:00Z"}}}
	})
	status := func(p, bearer string) knowledge.ArticleKnowledge {
		return kgDecode[knowledge.ArticleKnowledge](t, kgGet(h.wiki(), "/api/v1/knowledge/article?source_id=pub&path="+p, bearer))
	}
	if a := status("billing.md", "test-reader"); a.Status != knowledge.ArticleProcessing || a.Fingerprint != "" {
		t.Fatalf("before the pipeline is known: %+v", a)
	}
	if out := kgGet(h.wiki(), "/api/v1/knowledge/entities", "test-reader"); out.Code != 503 || kgErrorCode(t, out) != "knowledge_unavailable" {
		t.Fatalf("entities before the pipeline is known: %d", out.Code)
	}
	h.start()
	h.settle()
	if a := status("billing.md", "test-reader"); a.Status != knowledge.ArticleReady || a.Knowledge == nil || a.Fingerprint == "" {
		t.Fatalf("ready: %+v", a)
	}
	if a := status("rejected.md", "test-owner"); a.Status != knowledge.ArticleFailed || a.Error != knowledge.CodeRejected {
		t.Fatalf("owner failed: %+v", a)
	}
	if a := status("rejected.md", "test-reader"); a.Status != knowledge.ArticleFailed || a.Error != "" {
		t.Fatalf("reader failed: %+v", a)
	}
	if a := status("large.md", "test-reader"); a.Status != knowledge.ArticleTooLarge {
		t.Fatalf("too large: %+v", a)
	}
	// A new revision is processing (or unavailable) until indexed, and the
	// old graph is not shown as current.
	h.ex.setDown(true)
	h.put("pub", "billing.md", "# Billing\n\nledger uses PostgreSQL now.\n")
	a := status("billing.md", "test-reader")
	if a.Knowledge != nil || (a.Status != knowledge.ArticleProcessing && a.Status != knowledge.ArticleUnavailable) {
		t.Fatalf("new revision shown with old knowledge: %+v", a)
	}
	h.ex.setDown(false)
	h.settle()
	if a := status("billing.md", "test-reader"); a.Status != knowledge.ArticleReady || a.Knowledge.Entities[0].Name == "billing" {
		t.Fatalf("new revision: %+v", a)
	}
}
