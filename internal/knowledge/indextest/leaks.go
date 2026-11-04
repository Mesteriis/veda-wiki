package indextest

import (
	"errors"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// A claim whose end has no allowed mention (possible only in a hand-made
// graph) adds no empty node and no dangling edge to a subgraph.
func claimEndWithoutMentions(t *testing.T, newIndex Factory) {
	d := Doc{"pub", "ghost.md", "ghost", []string{"service:alpha"}, nil}
	k := Key(d)
	g := Graph(d, FP)
	ghost := knowledge.EntityID("service:ghost")
	g.Entities = append(g.Entities, knowledge.GraphEntity{ID: ghost, Type: "service", Key: "service:ghost"})
	g.Claims = append(g.Claims, knowledge.Claim{ID: "clm-ghost", Relation: "depends_on", HeadID: knowledge.EntityID("service:alpha"), TailID: ghost, Status: knowledge.ClaimExtracted,
		Evidence: knowledge.Evidence{SourceID: k.SourceID, Path: k.Path, SHA: k.SHA, Fingerprint: FP, BlockID: "b1", Quote: "alpha depends_on ghost", Start: 50, End: 60}})
	ix := newIndex(t)
	if err := ix.Publish(ctx, knowledge.Snapshot{Key: k, Fingerprint: FP}, g); err != nil {
		t.Fatal(err)
	}
	allowed := Public(Database(d))
	for _, depth := range []int{1, 2} {
		sub, err := ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: knowledge.EntityID("service:alpha")}, Depth: depth})
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, n := range sub.Nodes {
			if n.ID == "" || n.ID == ghost {
				t.Fatalf("depth %d: node without allowed mentions: %+v", depth, sub.Nodes)
			}
			ids[n.ID] = true
		}
		for _, e := range sub.Edges {
			if !ids[e.From] || !ids[e.To] {
				t.Fatalf("depth %d: dangling edge %+v", depth, e)
			}
		}
		if len(sub.Nodes) != 2 { // alpha and the document
			t.Fatalf("depth %d: nodes %+v", depth, sub.Nodes)
		}
	}
	if _, err := ix.Entity(ctx, allowed, FP, ghost, 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("entity without mentions readable", err)
	}
}

// Review Focus 1: a hidden claim between two entities that public documents
// mention separately does not connect them for a public reader.
func hiddenBridge(t *testing.T, newIndex Factory) {
	p1 := Doc{"pub", "p1.md", "p1", []string{"service:alpha"}, nil}
	p2 := Doc{"pub", "p2.md", "p2", []string{"service:beta"}, nil}
	bridge := Doc{"priv", "bridge.md", "bridge", []string{"service:alpha", "service:beta"}, [][3]string{{"alpha", "depends_on", "beta"}}}
	ix := newIndex(t)
	Publish(t, ix, p1, p2, bridge)
	db := Database(p1, p2, bridge)
	public := Public(db)
	alpha, beta := knowledge.EntityID("service:alpha"), knowledge.EntityID("service:beta")
	for _, depth := range []int{1, 2} {
		g, err := ix.Graph(ctx, public, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: alpha}, Depth: depth})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range g.Nodes {
			if n.ID == beta || n.Key != nil && n.Key.SourceID == "priv" {
				t.Fatalf("depth %d: hidden claim connected %+v", depth, g.Nodes)
			}
		}
		for _, e := range g.Edges {
			if e.Kind == knowledge.EdgeRelation {
				t.Fatalf("depth %d: hidden claim edge %+v", depth, e)
			}
		}
	}
	v, err := ix.Article(ctx, public, FP, Key(p1), 0)
	if err != nil || len(v.Related) != 0 || len(v.Claims) != 0 {
		t.Fatalf("related through a hidden claim %v %+v", err, v)
	}
	c, err := ix.Context(ctx, public, FP, knowledge.ContextQuery{Text: "alpha", Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range c.Items {
		if it.Kind != knowledge.ItemEntity || it.Entity.ID != alpha {
			t.Fatalf("context reached past a hidden claim: %+v", it)
		}
	}
	// The owner sees the claim and the connection.
	g, _ := ix.Graph(ctx, Owner(db), FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: alpha}, Depth: 2})
	found := false
	for _, n := range g.Nodes {
		found = found || n.ID == beta
	}
	if !found {
		t.Fatalf("owner graph misses the claim: %+v", g.Nodes)
	}
}
