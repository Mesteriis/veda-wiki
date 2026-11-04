// Package indextest is the shared contract suite of knowledge.Index. Every
// implementation (memindex, neo4jindex) runs Run against an empty index, so
// they answer every read the same way, including the Review Focus 1-3 leak
// cases; Parity compares an implementation with a reference answer by answer.
package indextest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Factory returns an empty index. The suite publishes its own fixtures.
type Factory func(t *testing.T) knowledge.Index

var (
	ctx = context.Background()
	// FP is the active fingerprint of every fixture.
	FP  = knowledge.Fingerprint("kg1-active")
	now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	treeA   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// Doc is one article text with the entities ("type:Surface") and claims
// (head surface, relation, tail surface) a pipeline would extract from it.
type Doc struct {
	Source, Path, Text string
	Names              []string
	Claims             [][3]string
}

// Key is the snapshot key of d.
func Key(d Doc) knowledge.SnapshotKey {
	return knowledge.SnapshotKey{SourceID: d.Source, Path: d.Path, SHA: domain.Digest([]byte(d.Text))}
}

// Graph builds the graph a pipeline would publish for d under f.
func Graph(d Doc, f knowledge.Fingerprint) knowledge.Graph {
	k := Key(d)
	g := knowledge.Graph{Entities: []knowledge.GraphEntity{}, Mentions: []knowledge.Mention{}, Claims: []knowledge.Claim{}}
	ids := map[string]string{}
	seen := map[string]bool{}
	for i, n := range d.Names {
		typ, text, _ := strings.Cut(n, ":")
		ck, res := knowledge.CanonicalKey(knowledge.Name{SourceID: d.Source, Type: typ, Text: text}, nil)
		id := knowledge.EntityID(ck)
		ids[text] = id
		if !seen[id] {
			seen[id] = true
			g.Entities = append(g.Entities, knowledge.GraphEntity{ID: id, Type: typ, Key: ck})
		}
		ev := knowledge.Evidence{SourceID: k.SourceID, Path: k.Path, SHA: k.SHA, Fingerprint: f, BlockID: "b1", Quote: text, Start: i * 10, End: i*10 + len([]rune(text)), SourceStart: i*10 + 2, SourceEnd: i*10 + 2 + len([]rune(text)), Exact: true}
		g.Mentions = append(g.Mentions, knowledge.Mention{EntityID: id, Type: typ, Text: text, Score: 0.9 - float64(i)/100, Resolution: res, Evidence: ev})
	}
	for i, c := range d.Claims {
		ev := knowledge.Evidence{SourceID: k.SourceID, Path: k.Path, SHA: k.SHA, Fingerprint: f, BlockID: "b1", Quote: c[0] + " " + c[1] + " " + c[2], Start: 100 + i, End: 110 + i, SourceStart: 120 + i, SourceEnd: 130 + i}
		claim := knowledge.Claim{ID: fmt.Sprintf("clm-%s-%d", k.SHA[:8], i), Relation: c[1], HeadID: ids[c[0]], TailID: ids[c[2]], HeadScore: 0.8, TailScore: 0.7, Status: knowledge.ClaimExtracted, Evidence: ev}
		if i%2 == 1 {
			score := 0.5
			claim.Score = &score
			claim.Status, claim.Markers = knowledge.ClaimUncertain, []string{"negation"}
		}
		g.Claims = append(g.Claims, claim)
	}
	return g
}

// Database is a store snapshot with the public sources "pub" and "pub2",
// the private source "priv" and the public GitHub source "gh", holding docs
// as single-revision articles.
func Database(docs ...Doc) domain.Database {
	db := domain.Database{
		Sources: map[string]domain.Source{
			"pub":  {ID: "pub", Kind: "wiki", Visibility: "public"},
			"pub2": {ID: "pub2", Kind: "wiki", Visibility: "public"},
			"priv": {ID: "priv", Kind: "wiki", Visibility: "private"},
			"gh":   {ID: "gh", Kind: "wiki", Visibility: "public", ConnectionID: "c1", ImportCommit: commitA},
		},
		Articles: map[string]domain.Article{},
		Connections: map[string]domain.GitHubConnection{
			"c1": {ID: "c1", Verification: "verified", VerifiedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), LastPull: commitA, TreeSHA: treeA},
		},
	}
	for _, d := range docs {
		db.Articles[d.Source+"/"+d.Path] = domain.Article{SourceID: d.Source, Path: d.Path, Versions: []domain.Revision{revision(1, d)}}
	}
	return db
}

func revision(n int, d Doc) domain.Revision {
	return domain.Revision{Number: n, Text: d.Text, SHA: Key(d).SHA, Created: "2026-11-01T00:00:00Z"}
}

// Public is the allowed set of a public request over db.
func Public(db domain.Database) knowledge.AllowedSet {
	return knowledge.Allowed(db, true, knowledge.Scope{}, now)
}

// Owner is the allowed set of the owner over db.
func Owner(db domain.Database) knowledge.AllowedSet {
	return knowledge.Allowed(db, false, knowledge.Scope{Owner: true}, now)
}

// Publish publishes docs under FP.
func Publish(t *testing.T, ix knowledge.Index, docs ...Doc) {
	t.Helper()
	for _, d := range docs {
		if err := ix.Publish(ctx, knowledge.Snapshot{Key: Key(d), Fingerprint: FP}, Graph(d, FP)); err != nil {
			t.Fatal(err)
		}
	}
}

// Fixture documents. The hidden document spells billing differently, adds
// mentions that would change ranking, and is the only evidence for SecretDB.
var (
	PubA   = Doc{"pub", "a.md", "a", []string{"service:billing", "technology:Redis"}, [][3]string{{"billing", "uses", "Redis"}}}
	PubB   = Doc{"pub", "b.md", "b", []string{"service:billing", "technology:Redis", "host:db1"}, [][3]string{{"billing", "runs_on", "db1"}}}
	Hidden = Doc{"priv", "h.md", "h", []string{"service:Billing", "service:BILLING", "host:db1", "host:db1", "technology:SecretDB"}, [][3]string{{"Billing", "depends_on", "SecretDB"}, {"Billing", "runs_on", "db1"}}}
)

// Ordering fixtures. Go orders strings by code point, Neo4j by UTF-16 code
// unit: a supplementary character (emoji) sorts after U+FF21 in Go but
// before it in Neo4j. The paths and block IDs below make every "first" or
// "top N" choice depend on that order, so an index that orders strings in
// Cypher fails Parity.
var (
	OrderA    = Doc{"pub", "\uFF21.md", "ordered fullwidth", []string{"service:ordered", "service:ordered"}, nil}
	OrderB    = Doc{"pub", "\U0001F680.md", "ordered emoji", []string{"service:ordered"}, nil}
	OrderedID = knowledge.EntityID("service:ordered")
)

// orderedGraph is Graph(d) with block IDs and resolution bases that differ
// per mention, so the first mention (the entity's resolution) and the
// mentions kept by a limit are visible in the answers.
func orderedGraph(d Doc) knowledge.Graph {
	g := Graph(d, FP)
	blocks := []string{"b\U0001F680", "b\uFF21"}
	for i := range g.Mentions {
		g.Mentions[i].Evidence.BlockID = blocks[i%len(blocks)]
		g.Mentions[i].Evidence.Start, g.Mentions[i].Evidence.End = 0, 7
		g.Mentions[i].Resolution.Basis = d.Path + "#" + blocks[i%len(blocks)]
	}
	return g
}

// Reads runs every query a reader can make around the fixtures and returns
// the answers (values and errors) in order.
func Reads(ix knowledge.Index, allowed knowledge.AllowedSet, extra ...knowledge.SnapshotKey) []any {
	billing := knowledge.EntityID("service:billing")
	var out []any
	add := func(v any, err error) { out = append(out, v, err) }
	add(ix.Entities(ctx, allowed, FP, knowledge.EntityQuery{}))
	add(ix.Entities(ctx, allowed, FP, knowledge.EntityQuery{Text: "bill"}))
	add(ix.Entities(ctx, allowed, FP, knowledge.EntityQuery{Type: "host", Limit: 1}))
	add(ix.Entities(ctx, allowed, FP, knowledge.EntityQuery{SourceID: "pub", Limit: 2, Offset: 1}))
	add(ix.Entity(ctx, allowed, FP, billing, 0))
	add(ix.Entity(ctx, allowed, FP, billing, 1))
	keys := append([]knowledge.SnapshotKey{Key(PubA), Key(PubB)}, extra...)
	for _, k := range keys {
		add(ix.Article(ctx, allowed, FP, k, 0))
		add(ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{Snapshot: &k}, Depth: 2}))
	}
	for _, depth := range []int{1, 2} {
		add(ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}, Depth: depth}))
	}
	add(ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}, Depth: 2, MaxNodes: 3, MaxEdges: 2}))
	add(ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}, Relations: []string{"runs_on"}, SourceIDs: []string{"pub"}}))
	add(ix.Context(ctx, allowed, FP, knowledge.ContextQuery{Text: "billing db1 SecretDB", Seeds: append([]knowledge.SnapshotKey{Key(PubB), Key(Hidden)}, extra...), Depth: 2}))
	add(ix.Context(ctx, allowed, FP, knowledge.ContextQuery{Text: "billing", LimitBytes: 600, MaxItems: 3}))
	return out
}

func ptr[T any](v T) *T { return &v }
