package memindex_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
)

var (
	ctx = context.Background()
	fp  = knowledge.Fingerprint("kg1-active")
	now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
)

type doc struct {
	source, path, text string
	names              []string    // "type:Surface"
	claims             [][3]string // head surface, relation, tail surface
}

func key(d doc) knowledge.SnapshotKey {
	return knowledge.SnapshotKey{SourceID: d.source, Path: d.path, SHA: domain.Digest([]byte(d.text))}
}

// graph builds the graph a pipeline would publish for d.
func graph(d doc, f knowledge.Fingerprint) knowledge.Graph {
	k := key(d)
	g := knowledge.Graph{}
	ids := map[string]string{}
	for i, n := range d.names {
		typ, text, _ := strings.Cut(n, ":")
		ck, res := knowledge.CanonicalKey(knowledge.Name{SourceID: d.source, Type: typ, Text: text}, nil)
		id := knowledge.EntityID(ck)
		ids[text] = id
		g.Entities = append(g.Entities, knowledge.GraphEntity{ID: id, Type: typ, Key: ck})
		ev := knowledge.Evidence{SourceID: k.SourceID, Path: k.Path, SHA: k.SHA, Fingerprint: f, BlockID: "b1", Quote: text, Start: i * 10, End: i*10 + len([]rune(text)), Exact: true}
		g.Mentions = append(g.Mentions, knowledge.Mention{EntityID: id, Type: typ, Text: text, Score: 0.9, Resolution: res, Evidence: ev})
	}
	for i, c := range d.claims {
		ev := knowledge.Evidence{SourceID: k.SourceID, Path: k.Path, SHA: k.SHA, Fingerprint: f, BlockID: "b1", Quote: c[0] + " " + c[1] + " " + c[2], Start: 100 + i, End: 110 + i}
		g.Claims = append(g.Claims, knowledge.Claim{ID: fmt.Sprintf("clm-%s-%d", k.SHA[:8], i), Relation: c[1], HeadID: ids[c[0]], TailID: ids[c[2]], Status: knowledge.ClaimExtracted, Evidence: ev})
	}
	return g
}

func database(docs ...doc) domain.Database {
	db := domain.Database{Sources: map[string]domain.Source{
		"pub":  {ID: "pub", Kind: "wiki", Visibility: "public"},
		"pub2": {ID: "pub2", Kind: "wiki", Visibility: "public"},
		"priv": {ID: "priv", Kind: "wiki", Visibility: "private"},
	}, Articles: map[string]domain.Article{}}
	for _, d := range docs {
		db.Articles[d.source+"/"+d.path] = domain.Article{SourceID: d.source, Path: d.path, Versions: []domain.Revision{{Number: 1, Text: d.text, SHA: key(d).SHA}}}
	}
	return db
}

func index(t *testing.T, docs ...doc) *memindex.Index {
	t.Helper()
	ix := memindex.New()
	for _, d := range docs {
		if err := ix.Publish(ctx, knowledge.Snapshot{Key: key(d), Fingerprint: fp}, graph(d, fp)); err != nil {
			t.Fatal(err)
		}
	}
	return ix
}

var (
	pubA = doc{"pub", "a.md", "a", []string{"service:billing", "technology:Redis"}, [][3]string{{"billing", "uses", "Redis"}}}
	pubB = doc{"pub", "b.md", "b", []string{"service:billing", "technology:Redis", "host:db1"}, [][3]string{{"billing", "runs_on", "db1"}}}
	// The hidden document spells billing differently, adds mentions that
	// would change ranking, and is the only evidence for SecretDB.
	hidden = doc{"priv", "h.md", "h", []string{"service:Billing", "service:BILLING", "host:db1", "host:db1", "technology:SecretDB"}, [][3]string{{"Billing", "depends_on", "SecretDB"}, {"Billing", "runs_on", "db1"}}}
)

// reads runs every query a public reader can make and returns the answers.
func reads(t *testing.T, ix *memindex.Index, allowed knowledge.AllowedSet) []any {
	t.Helper()
	billing := knowledge.EntityID("service:billing")
	var out []any
	page, err := ix.Entities(ctx, allowed, fp, knowledge.EntityQuery{})
	out = append(out, page, err)
	page, err = ix.Entities(ctx, allowed, fp, knowledge.EntityQuery{Text: "bill"})
	out = append(out, page, err)
	card, err := ix.Entity(ctx, allowed, fp, billing, 0)
	out = append(out, card, err)
	art, err := ix.Article(ctx, allowed, fp, key(pubA), 0)
	out = append(out, art, err)
	for _, depth := range []int{1, 2} {
		g, err := ix.Graph(ctx, allowed, fp, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}, Depth: depth})
		out = append(out, g, err)
	}
	c, err := ix.Context(ctx, allowed, fp, knowledge.ContextQuery{Text: "billing db1 SecretDB", Seeds: []knowledge.SnapshotKey{key(pubB), key(hidden)}, Depth: 2})
	out = append(out, c, err)
	return out
}

// Review Focus 1 at the index level: publishing a hidden document changes
// no answer a public reader gets — names, aliases, counts, ranking,
// claims, related articles, traversal and context are all identical.
func TestHiddenDocumentDoesNotInfluencePublicReads(t *testing.T) {
	public := knowledge.Allowed(database(pubA, pubB, hidden), true, knowledge.Scope{}, now)
	if public.Has(key(hidden)) || !public.Has(key(pubA)) {
		t.Fatal("fixture allowed set")
	}
	without := reads(t, index(t, pubA, pubB), public)
	with := reads(t, index(t, pubA, pubB, hidden), public)
	if !reflect.DeepEqual(with, without) {
		t.Fatalf("hidden document changed public reads:\n%+v\n%+v", with, without)
	}
	card := with[4].(knowledge.EntityCard)
	if card.Name != "billing" || len(card.Aliases) != 0 || card.Documents != 2 || card.EntitySummary.Mentions != 2 {
		t.Fatalf("public card %+v", card)
	}
	ix := index(t, pubA, pubB, hidden)
	if _, err := ix.Entity(ctx, public, fp, knowledge.EntityID("technology@priv:secretdb"), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("hidden-only entity readable", err)
	}
	if _, err := ix.Article(ctx, public, fp, key(hidden), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("hidden article readable", err)
	}
	if _, err := ix.Graph(ctx, public, fp, knowledge.GraphQuery{Center: knowledge.GraphCenter{Snapshot: ptr(key(hidden))}}); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("hidden graph center readable", err)
	}
	// The owner sees the hidden evidence, with its spellings as aliases.
	owner := knowledge.Allowed(database(pubA, pubB, hidden), false, knowledge.Scope{Owner: true}, now)
	ownerCard, err := ix.Entity(ctx, owner, fp, knowledge.EntityID("service:billing"), 0)
	if err != nil || ownerCard.Documents != 3 || !reflect.DeepEqual(ownerCard.Aliases, []string{"BILLING", "Billing"}) {
		t.Fatalf("owner card %v %+v", err, ownerCard)
	}
	// Another fingerprint is never read.
	if _, err = ix.Entity(ctx, owner, "kg1-other", knowledge.EntityID("service:billing"), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("other generation readable")
	}
}

func ptr[T any](v T) *T { return &v }

func TestRelatedArticlesExplainAndSkipGenericEntities(t *testing.T) {
	var docs []doc
	for i := range 6 {
		docs = append(docs, doc{"pub", fmt.Sprintf("d%d.md", i), fmt.Sprintf("d%d", i), []string{"technology:API"}, nil})
	}
	docs[0].names = append(docs[0].names, "service:ledger", "technology:PostgreSQL", "person:Иван")
	docs[0].claims = [][3]string{{"ledger", "uses", "PostgreSQL"}}
	docs[1].names = append(docs[1].names, "technology:PostgreSQL")
	docs[2].names = append(docs[2].names, "service:ledger")
	other := doc{"pub2", "x.md", "x", []string{"person:Иван"}, nil}
	docs = append(docs, other)
	ix := index(t, docs...)
	allowed := knowledge.Allowed(database(docs...), true, knowledge.Scope{}, now)
	v, err := ix.Article(ctx, allowed, fp, key(docs[0]), 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string][]knowledge.ReasonKind{}
	for _, r := range v.Related {
		for _, reason := range r.Reasons {
			if reason.EntityID == knowledge.EntityID("technology@pub:api") {
				t.Fatal("generic entity used as a reason")
			}
			if len(reason.Evidence) == 0 {
				t.Fatal("reason without evidence")
			}
			kinds[r.Key.Path] = append(kinds[r.Key.Path], reason.Kind)
		}
	}
	want := map[string][]knowledge.ReasonKind{
		"d1.md": {knowledge.ReasonSharedEntity, knowledge.ReasonRelation},
		"d2.md": {knowledge.ReasonSharedEntity, knowledge.ReasonRelation},
		"x.md":  {knowledge.ReasonCandidate},
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("related %v", kinds)
	}
	card, _ := ix.Entity(ctx, allowed, fp, knowledge.EntityID("person@pub:иван"), 0)
	if len(card.Candidates) != 1 || card.Candidates[0].Resolution.Method != knowledge.ResolveCandidate {
		t.Fatalf("candidates %+v", card.Candidates)
	}
	// Without the other source's document, no candidate is suggested.
	scoped := knowledge.Allowed(database(docs[:6]...), true, knowledge.Scope{}, now)
	if card, _ = ix.Entity(ctx, scoped, fp, knowledge.EntityID("person@pub:иван"), 0); len(card.Candidates) != 0 {
		t.Fatal("candidate from a document outside the allowed set")
	}
}

func TestGraphBoundsAndFilters(t *testing.T) {
	ix := index(t, pubA, pubB)
	allowed := knowledge.Allowed(database(pubA, pubB), false, knowledge.Scope{}, now)
	billing := knowledge.EntityID("service:billing")
	g, err := ix.Graph(ctx, allowed, fp, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}})
	if err != nil || len(g.Nodes) != 5 || g.Truncated { // billing, Redis, db1, a, b
		t.Fatalf("depth 1: %v %+v", err, g.Nodes)
	}
	g, _ = ix.Graph(ctx, allowed, fp, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}, MaxNodes: 2, MaxEdges: 1})
	if len(g.Nodes) != 2 || len(g.Edges) > 1 || !g.Truncated {
		t.Fatalf("caps: %+v", g)
	}
	g, _ = ix.Graph(ctx, allowed, fp, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}, Relations: []string{"uses"}, Depth: 9})
	for _, e := range g.Edges {
		if e.Kind == knowledge.EdgeRelation && e.Relation != "uses" {
			t.Fatalf("relation filter: %+v", e)
		}
	}
	g, _ = ix.Graph(ctx, allowed, fp, knowledge.GraphQuery{Center: knowledge.GraphCenter{Snapshot: ptr(key(pubA))}, SourceIDs: []string{"other"}})
	if len(g.Nodes) != 0 && g.Nodes[0].Kind == knowledge.NodeDocument {
		t.Fatalf("source filter: %+v", g)
	}
}

func TestContextSelectsWithReasonsWithinBytes(t *testing.T) {
	ix := index(t, pubA, pubB)
	allowed := knowledge.Allowed(database(pubA, pubB), false, knowledge.Scope{}, now)
	c, err := ix.Context(ctx, allowed, fp, knowledge.ContextQuery{Text: "where does billing run", Seeds: []knowledge.SnapshotKey{key(pubB)}})
	if err != nil || len(c.Items) == 0 || c.Items[0].Reason != knowledge.ReasonEntityMatch || c.Items[0].Entity.Name != "billing" {
		t.Fatalf("%v %+v", err, c)
	}
	reasons := map[string]bool{}
	for _, it := range c.Items {
		reasons[it.Reason] = true
	}
	if !reasons[knowledge.ReasonLexicalHit] || !reasons[knowledge.ReasonRelatedToEntity] {
		t.Fatalf("reasons %v", reasons)
	}
	small, _ := ix.Context(ctx, allowed, fp, knowledge.ContextQuery{Text: "billing", LimitBytes: 400})
	if !small.Truncated || small.Bytes > 400 {
		t.Fatalf("byte limit %+v", small)
	}
}

func TestPublishReplacesAtomicallyAndRetireKeepsSharedEntities(t *testing.T) {
	ix := index(t, pubA, pubB)
	snapA := knowledge.Snapshot{Key: key(pubA), Fingerprint: fp}
	if err := ix.Publish(ctx, snapA, graph(pubA, fp)); err != nil {
		t.Fatal(err)
	}
	allowed := knowledge.Allowed(database(pubA, pubB), false, knowledge.Scope{}, now)
	if v, _ := ix.Article(ctx, allowed, fp, key(pubA), 0); len(v.Mentions) != 2 || len(v.Claims) != 1 {
		t.Fatalf("republish duplicated %+v", v)
	}
	if err := ix.Retire(ctx, []knowledge.Snapshot{snapA}); err != nil {
		t.Fatal(err)
	}
	card, err := ix.Entity(ctx, allowed, fp, knowledge.EntityID("service:billing"), 0)
	if err != nil || card.Documents != 1 {
		t.Fatal("shared entity lost", err)
	}
	ix.SetUnavailable(errors.New("down"))
	if ix.Status(ctx) == nil || ix.Publish(ctx, snapA, knowledge.Graph{}) == nil {
		t.Fatal("outage not reported")
	}
}
