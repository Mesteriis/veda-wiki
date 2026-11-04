package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Review Focus 3 / §12-8: the public graph of an article is built from its
// published revision, the owner's and reader's from the current one; the
// owner may also read the publication copy by its SHA, nobody else can.
func TestKnowledgePublishedAndCurrentRevisionsDiffer(t *testing.T) {
	h := newKGHarness(t)
	h.seed()
	h.start()
	h.settle()
	articleURL := "/api/v1/knowledge/article?source_id=gh&path=page.md"
	publicView := kgDecode[knowledge.ArticleKnowledge](t, kgGet(h.public(), articleURL, "test-owner"))
	ownerView := kgDecode[knowledge.ArticleKnowledge](t, kgGet(h.wiki(), articleURL, "test-owner"))
	readerView := kgDecode[knowledge.ArticleKnowledge](t, kgGet(h.wiki(), articleURL, "test-reader"))
	publishedSHA, draftSHA := domain.Digest([]byte(kgPublished)), domain.Digest([]byte(kgDraft))
	if publicView.Key.SHA != publishedSHA || ownerView.Key.SHA != draftSHA || readerView.Key.SHA != draftSHA {
		t.Fatalf("visible SHAs: public %s owner %s reader %s", publicView.Key.SHA, ownerView.Key.SHA, readerView.Key.SHA)
	}
	names := func(v knowledge.ArticleKnowledge) string {
		if v.Status != knowledge.ArticleReady || v.Knowledge == nil {
			t.Fatalf("article not ready: %+v", v)
		}
		var out []string
		for _, e := range v.Knowledge.Entities {
			out = append(out, e.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(publicView); got != "Redis,billing" && got != "billing,Redis" {
		t.Fatalf("public graph from the published revision: %s", got)
	}
	if got := names(ownerView); !strings.Contains(got, "ledger") || strings.Contains(got, "billing") {
		t.Fatalf("owner graph from the current revision: %s", got)
	}
	publicBody := kgGet(h.public(), articleURL, "").Body.String()
	if strings.Contains(publicBody, "ledger") || strings.Contains(publicBody, draftSHA) || strings.Contains(publicBody, `"revision"`) {
		t.Fatalf("public answer exposed the private draft: %s", publicBody)
	}
	// Public graphs centred on the article differ the same way.
	graphURL := "/api/v1/knowledge/graph?source_id=gh&path=page.md"
	if g := kgGet(h.public(), graphURL, "").Body.String(); !strings.Contains(g, "billing") || strings.Contains(g, "ledger") {
		t.Fatalf("public graph: %s", g)
	}
	if g := kgGet(h.wiki(), graphURL, "test-owner").Body.String(); !strings.Contains(g, "ledger") || strings.Contains(g, "billing") {
		t.Fatalf("owner graph: %s", g)
	}
	// The owner may read the publication copy explicitly; reader, scoped
	// key and public get the same 404 as an unknown SHA.
	copyURL := articleURL + "&sha256=" + publishedSHA
	if v := kgDecode[knowledge.ArticleKnowledge](t, kgGet(h.wiki(), copyURL, "test-owner")); v.Key.SHA != publishedSHA || !strings.Contains(names(v), "billing") {
		t.Fatalf("owner publication copy: %+v", v)
	}
	// That read sees the owner's current revisions with only this article
	// swapped for its copy: the draft never appears beside it.
	copyView := kgDecode[knowledge.ArticleKnowledge](t, kgGet(h.wiki(), copyURL, "test-owner"))
	for _, rel := range copyView.Knowledge.Related {
		if rel.Key.SourceID == "gh" && rel.Key.Path == "page.md" {
			t.Fatalf("publication copy related to its own draft: %+v", rel.Key)
		}
	}
	copyGraph := kgDecode[knowledge.Subgraph](t, kgGet(h.wiki(), graphURL+"&depth=2&sha256="+publishedSHA, "test-owner"))
	seen := map[string]bool{}
	for _, n := range copyGraph.Nodes {
		if n.Key == nil {
			continue
		}
		article := n.Key.SourceID + "/" + n.Key.Path
		if seen[article] || n.Key.SHA == draftSHA {
			t.Fatalf("article twice or draft in the copy graph: %+v", copyGraph.Nodes)
		}
		seen[article] = true
	}
	if !seen["gh/page.md"] || !seen["pub/billing.md"] {
		t.Fatalf("copy graph lost the owner's current revisions: %v", seen)
	}
	unknown := kgGet(h.wiki(), articleURL+"&sha256="+strings.Repeat("0", 64), "test-owner")
	for name, out := range map[string]*httptest.ResponseRecorder{
		"reader": kgGet(h.wiki(), copyURL, "test-reader"),
		"public": kgGet(h.public(), articleURL+"&sha256="+draftSHA, "test-owner"),
	} {
		if out.Code != 404 || out.Body.String() != unknown.Body.String() {
			t.Fatalf("%s read another revision: %d %s", name, out.Code, out.Body.String())
		}
	}
}

// kgSnapshot captures the answers of every read route for one caller; each
// must be a non-empty answer, so a comparison is never vacuous.
func kgSnapshot(t *testing.T, w Wiki, bearer string, paths []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range paths {
		r := kgGet(w, p, bearer)
		out[p] = r.Body.String()
		if r.Code != 200 || strings.Contains(r.Body.String(), `"items":[]`) || strings.Contains(r.Body.String(), `"entities":[]`) {
			t.Fatalf("%s: %d %s", p, r.Code, r.Body.String())
		}
	}
	return out
}

// Review Focus 1 / §12-9: documents a caller may not read (a private source
// for the public, another source for a scoped key) never change any answer:
// entity names, aliases, counts, ranking, related reasons, graph
// intermediates and context are byte-identical with and without them.
func TestKnowledgeHiddenDocumentsDoNotInfluenceAnswers(t *testing.T) {
	h := newKGHarness(t)
	h.seed()
	h.put("alpha", "hosts.md", "# Hosts\n\nbilling runs on db1.\n")
	h.start()
	h.settle()
	db1 := kgEntityID(t, h.public(), "", "db1", "")
	alphaBilling := kgEntityID(t, h.wiki(), h.scoped, "billing", "alpha")
	paths := func(entity string, ref string) []string {
		return []string{
			"/api/v1/knowledge/entities", "/api/v1/knowledge/entities?q=db", "/api/v1/knowledge/entities?type=host",
			"/api/v1/knowledge/entity?id=" + entity, "/api/v1/knowledge/entity?id=" + db1,
			"/api/v1/knowledge/article?" + ref,
			"/api/v1/knowledge/graph?entity_id=" + db1 + "&depth=2", "/api/v1/knowledge/graph?" + ref + "&depth=2",
			"/api/v1/knowledge/context?q=" + url.QueryEscape("billing db1 Redis") + "&depth=2",
			"/api/v1/knowledge/context?q=DB1",
		}
	}
	pubBilling := kgEntityID(t, h.public(), "", "billing", "pub")
	publicPaths := paths(pubBilling, "source_id=pub&path=billing.md")
	scopedPaths := paths(alphaBilling, "source_id=alpha&path=hosts.md")
	publicBefore := kgSnapshot(t, h.public(), "", publicPaths)
	ownerTokenOnPublic := kgSnapshot(t, h.public(), "test-owner", publicPaths)
	scopedBefore := kgSnapshot(t, h.wiki(), h.scoped, scopedPaths)
	for p, body := range publicBefore {
		if ownerTokenOnPublic[p] != body {
			t.Fatalf("public listener honoured the owner token on %s", p)
		}
	}
	// Hidden documents: DB1 written in capitals many times (it would rename
	// db1 and change its counts), a hidden service between db1 and Redis
	// (a graph intermediate), and the same names in the private sources.
	hidden := "# Secret\n\nsecret-svc runs on DB1 DB1 DB1 and uses Redis; billing too.\n"
	source(t, h.s, "priv", "private")
	h.put("priv", "secret.md", hidden)
	h.put("beta", "secret.md", hidden)
	h.settle()
	owner := kgGet(h.wiki(), "/api/v1/knowledge/entity?id="+db1, "test-owner").Body.String()
	if !strings.Contains(owner, "DB1") || !strings.Contains(owner, "priv") {
		t.Fatalf("hidden document was not indexed, the comparison would prove nothing: %s", owner)
	}
	for p, body := range kgSnapshot(t, h.public(), "", publicPaths) {
		if body != publicBefore[p] {
			t.Errorf("public answer changed by a hidden document: %s\nbefore %s\nafter  %s", p, publicBefore[p], body)
		}
	}
	for p, body := range kgSnapshot(t, h.wiki(), h.scoped, scopedPaths) {
		if body != scopedBefore[p] {
			t.Errorf("scoped answer changed by another source: %s\nbefore %s\nafter  %s", p, scopedBefore[p], body)
		}
		if strings.Contains(body, "secret") || strings.Contains(body, "beta") || strings.Contains(body, `"pub"`) {
			t.Errorf("scoped answer shows another source: %s %s", p, body)
		}
	}
	// A hidden-only entity is the same 404 as an unknown one.
	secret := kgEntityID(t, h.wiki(), "test-owner", "secret-svc", "")
	unknown := kgGet(h.public(), "/api/v1/knowledge/entity?id=ent-00000000000000000000", "")
	for name, out := range map[string]*httptest.ResponseRecorder{
		"public":     kgGet(h.public(), "/api/v1/knowledge/entity?id="+secret, ""),
		"scoped key": kgGet(h.wiki(), "/api/v1/knowledge/entity?id="+secret, h.scoped),
		"graph":      kgGet(h.wiki(), "/api/v1/knowledge/graph?entity_id="+secret, h.scoped),
		"article":    kgGet(h.wiki(), "/api/v1/knowledge/article?source_id=priv&path=secret.md", h.scoped),
	} {
		if out.Code != 404 || out.Body.String() != unknown.Body.String() {
			t.Errorf("%s: hidden id answered %d %s", name, out.Code, out.Body.String())
		}
	}
}

// §12-2: equal paths in two sources stay two articles, and an equal
// non-technical name in two sources stays two entities: the other one is
// only a candidate, and only for a caller who may read both sources.
func TestKnowledgeSamePathsAndNamesAreNotMerged(t *testing.T) {
	h := newKGHarness(t)
	h.seed()
	h.start()
	h.settle()
	page := kgDecode[knowledge.EntityPage](t, kgGet(h.wiki(), "/api/v1/knowledge/entities?q=Redis&type=technology", "test-owner"))
	if len(page.Entities) != 2 || page.Entities[0].ID == page.Entities[1].ID {
		t.Fatalf("Redis of pub and alpha merged: %+v", page.Entities)
	}
	pubRedis := kgEntityID(t, h.wiki(), "test-owner", "Redis", "pub")
	alphaRedis := kgEntityID(t, h.wiki(), "test-owner", "Redis", "alpha")
	card := kgDecode[knowledge.EntityCard](t, kgGet(h.wiki(), "/api/v1/knowledge/entity?id="+pubRedis, "test-owner"))
	if len(card.Candidates) != 1 || card.Candidates[0].EntityID != alphaRedis || card.Candidates[0].Resolution.Method != knowledge.ResolveCandidate {
		t.Fatalf("owner candidate: %+v", card.Candidates)
	}
	for _, d := range card.DocumentList {
		if d.SourceID != "pub" {
			t.Fatalf("candidate evidence merged into the entity: %+v", card.DocumentList)
		}
	}
	public := kgGet(h.public(), "/api/v1/knowledge/entity?id="+pubRedis, "").Body.String()
	if strings.Contains(public, alphaRedis) || strings.Contains(public, "alpha") {
		t.Fatalf("public saw a candidate from a private source: %s", public)
	}
	pub := kgDecode[knowledge.ArticleKnowledge](t, kgGet(h.wiki(), "/api/v1/knowledge/article?source_id=pub&path=guide.md", "test-owner"))
	alpha := kgDecode[knowledge.ArticleKnowledge](t, kgGet(h.wiki(), "/api/v1/knowledge/article?source_id=alpha&path=guide.md", "test-owner"))
	if pub.Key.SourceID != "pub" || alpha.Key.SourceID != "alpha" || pub.Key.SHA == alpha.Key.SHA {
		t.Fatalf("same path in two sources merged: %+v %+v", pub.Key, alpha.Key)
	}
	for _, m := range alpha.Knowledge.Mentions {
		if m.Evidence.SourceID != "alpha" {
			t.Fatalf("article view mixed sources: %+v", m.Evidence)
		}
	}
}

// §12-10 and §12-9 for sessions: access closes on the request that follows
// the change, without reindexing: expired and revoked keys, a team grant
// change, a visibility change, a partial import and an expired GitHub
// visibility proof.
func TestKnowledgeAccessClosesImmediately(t *testing.T) {
	h := newKGHarness(t)
	h.seed()
	h.start()
	h.settle()
	alphaArticle := "/api/v1/knowledge/article?source_id=alpha&path=guide.md"
	ledgerSearch := "/api/v1/knowledge/entities?q=ledger"
	hasLedger := func(w Wiki, bearer string) bool {
		return strings.Contains(kgGet(w, ledgerSearch, bearer).Body.String(), "ledger")
	}
	// Scoped key: reads alpha, then expires.
	if kgGet(h.wiki(), alphaArticle, h.scoped).Code != 200 || !hasLedger(h.wiki(), h.scoped) {
		t.Fatal("scoped key cannot read its source")
	}
	h.s.mutate(t, func(db *domain.Database) {
		k := db.AccessKeys["agent"]
		k.Expires = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
		db.AccessKeys["agent"] = k
	})
	if kgGet(h.wiki(), alphaArticle, h.scoped).Code != 404 || hasLedger(h.wiki(), h.scoped) {
		t.Fatal("expired key still reads knowledge")
	}
	// A second key is revoked.
	second, err := h.s.auth().CreateAccessKey("second", []string{"alpha"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !hasLedger(h.wiki(), second) {
		t.Fatal("second key cannot read")
	}
	if err = h.s.auth().RevokeAccessKey("second"); err != nil {
		t.Fatal(err)
	}
	if kgGet(h.wiki(), alphaArticle, second).Code != 404 || hasLedger(h.wiki(), second) {
		t.Fatal("revoked key still reads knowledge")
	}
	// A team user loses alpha when the team's grant changes to beta.
	h.s.mutate(t, func(db *domain.Database) {
		db.Teams = map[string]domain.WikiTeam{"ops": {ID: "ops", Sources: []string{"alpha"}, Revision: 1}}
		db.Users = map[string]domain.WikiUser{"person": {ID: "person", Teams: []string{"ops"}, Revision: 1, AuthVersion: 1}}
	})
	cookie, err := h.sessions.Issue(auth.Login{Now: time.Now(), Authenticate: func() (auth.Grant, bool) {
		return auth.Grant{Named: true, User: auth.SessionUser{ID: "person", AuthVersion: 1}}, true
	}})
	if err != nil {
		t.Fatal(err)
	}
	userGet := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://localhost:8080"+path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.AddCookie(&http.Cookie{Name: auth.ReaderCookie, Value: cookie})
		out := httptest.NewRecorder()
		h.wiki().ServeHTTP(out, r)
		return out
	}
	if userGet(alphaArticle).Code != 200 || !strings.Contains(userGet(ledgerSearch).Body.String(), "ledger") {
		t.Fatal("team user cannot read its team's source")
	}
	h.s.mutate(t, func(db *domain.Database) {
		db.Teams["ops"] = domain.WikiTeam{ID: "ops", Sources: []string{"beta"}, Revision: 2}
	})
	if userGet(alphaArticle).Code != 404 || strings.Contains(userGet(ledgerSearch).Body.String(), "ledger") {
		t.Fatal("team grant change did not close access")
	}
	// Public: visibility change, partial import, expired GitHub proof.
	pubArticle := "/api/v1/knowledge/article?source_id=pub&path=billing.md"
	ghArticle := "/api/v1/knowledge/article?source_id=gh&path=page.md"
	closes := map[string]struct {
		path   string
		mutate func(db *domain.Database)
	}{
		"visibility": {pubArticle, func(db *domain.Database) { s := db.Sources["pub"]; s.Visibility = "private"; db.Sources["pub"] = s }},
		"partial":    {pubArticle, func(db *domain.Database) { s := db.Sources["pub"]; s.ImportPartial = true; db.Sources["pub"] = s }},
		"proof": {ghArticle, func(db *domain.Database) {
			c := db.Connections["c1"]
			c.VerifiedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			db.Connections["c1"] = c
		}},
	}
	for name, c := range closes {
		before := h.s.state(t)
		if kgGet(h.public(), c.path, "").Code != 200 {
			t.Fatalf("%s: public article unreadable before the change", name)
		}
		h.s.mutate(t, c.mutate)
		if out := kgGet(h.public(), c.path, ""); out.Code != 404 {
			t.Fatalf("%s: public still reads knowledge: %d", name, out.Code)
		}
		if body := kgGet(h.public(), "/api/v1/knowledge/entities", "").Body.String(); name != "proof" && strings.Contains(body, "db1") {
			t.Fatalf("%s: public entities still list the source: %s", name, body)
		}
		h.s.mutate(t, func(db *domain.Database) { db.Sources, db.Connections = before.Sources, before.Connections })
	}
}
