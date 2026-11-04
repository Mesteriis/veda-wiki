package indextest

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

func graphBounds(t *testing.T, newIndex Factory) {
	ix := newIndex(t)
	Publish(t, ix, PubA, PubB)
	allowed := Public(Database(PubA, PubB))
	billing := knowledge.EntityID("service:billing")
	g, err := ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}})
	if err != nil || len(g.Nodes) != 5 || g.Truncated { // billing, Redis, db1, a, b
		t.Fatalf("depth 1: %v %+v", err, g.Nodes)
	}
	for _, e := range g.Edges {
		if len(e.Evidence) == 0 || e.Kind == knowledge.EdgeMention && e.Count == 0 {
			t.Fatalf("edge without evidence or count: %+v", e)
		}
	}
	g, _ = ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}, MaxNodes: 2, MaxEdges: 1})
	if len(g.Nodes) != 2 || len(g.Edges) > 1 || !g.Truncated {
		t.Fatalf("caps: %+v", g)
	}
	g, _ = ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}, Relations: []string{"uses"}, Depth: 9})
	for _, e := range g.Edges {
		if e.Kind == knowledge.EdgeRelation && e.Relation != "uses" {
			t.Fatalf("relation filter: %+v", e)
		}
	}
	if _, err = ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{Snapshot: ptr(Key(PubA))}, SourceIDs: []string{"other"}}); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("document center outside the source filter", err)
	}
	if _, err = ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: "ent-unknown"}}); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("unknown center", err)
	}
}

func contextSelects(t *testing.T, newIndex Factory) {
	ix := newIndex(t)
	Publish(t, ix, PubA, PubB)
	allowed := Public(Database(PubA, PubB))
	c, err := ix.Context(ctx, allowed, FP, knowledge.ContextQuery{Text: "where does billing run", Seeds: []knowledge.SnapshotKey{Key(PubB)}})
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
	small, _ := ix.Context(ctx, allowed, FP, knowledge.ContextQuery{Text: "billing", LimitBytes: 400})
	if !small.Truncated || small.Bytes > 400 {
		t.Fatalf("byte limit %+v", small)
	}
}

func publishAndRetire(t *testing.T, newIndex Factory) {
	ix := newIndex(t)
	Publish(t, ix, PubA, PubB)
	snapA := knowledge.Snapshot{Key: Key(PubA), Fingerprint: FP}
	if err := ix.Publish(ctx, snapA, Graph(PubA, FP)); err != nil {
		t.Fatal(err)
	}
	allowed := Public(Database(PubA, PubB))
	v, err := ix.Article(ctx, allowed, FP, Key(PubA), 0)
	if err != nil || len(v.Mentions) != 2 || len(v.Claims) != 1 {
		t.Fatalf("republish duplicated %v %+v", err, v)
	}
	// Republishing with another graph replaces the previous one.
	changed := PubA
	changed.Names = []string{"service:billing"}
	changed.Claims = nil
	if err = ix.Publish(ctx, snapA, Graph(changed, FP)); err != nil {
		t.Fatal(err)
	}
	if v, _ = ix.Article(ctx, allowed, FP, Key(PubA), 0); len(v.Mentions) != 1 || len(v.Claims) != 0 {
		t.Fatalf("republish did not replace %+v", v)
	}
	// Another generation of the same text is a separate snapshot.
	other := knowledge.Snapshot{Key: Key(PubA), Fingerprint: "kg1-next"}
	if err = ix.Publish(ctx, other, Graph(PubA, "kg1-next")); err != nil {
		t.Fatal(err)
	}
	if v, _ = ix.Article(ctx, allowed, FP, Key(PubA), 0); len(v.Mentions) != 1 {
		t.Fatalf("generations mixed %+v", v)
	}
	if err = ix.Retire(ctx, []knowledge.Snapshot{snapA, other, {Key: knowledge.SnapshotKey{SourceID: "pub", Path: "none.md", SHA: "x"}, Fingerprint: FP}}); err != nil {
		t.Fatal(err)
	}
	card, err := ix.Entity(ctx, allowed, FP, knowledge.EntityID("service:billing"), 0)
	if err != nil || card.Documents != 1 || len(card.DocumentList) != 1 || card.DocumentList[0] != Key(PubB) {
		t.Fatal("shared entity lost", err, card)
	}
	if _, err = ix.Article(ctx, allowed, FP, Key(PubA), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("retired snapshot readable", err)
	}
	snaps, err := ix.Snapshots(ctx)
	if err != nil || !reflect.DeepEqual(snaps, []knowledge.Snapshot{{Key: Key(PubB), Fingerprint: FP}}) {
		t.Fatalf("snapshots %v %+v", err, snaps)
	}
	if err = ix.Status(ctx); err != nil {
		t.Fatal(err)
	}
}

func paging(t *testing.T, newIndex Factory) {
	ix := newIndex(t)
	var docs []Doc
	for i := range 4 {
		names := []string{"service:billing"}
		for j := range 3 {
			names = append(names, fmt.Sprintf("host:h%d-%d", i, j))
		}
		docs = append(docs, Doc{"pub", fmt.Sprintf("p%d.md", i), fmt.Sprintf("p%d", i), names, nil})
	}
	Publish(t, ix, docs...)
	allowed := Public(Database(docs...))
	page, err := ix.Entities(ctx, allowed, FP, knowledge.EntityQuery{Limit: 5})
	if err != nil || page.Total != 13 || len(page.Entities) != 5 || page.Next != 5 || page.Entities[0].Name != "billing" || page.Entities[0].Documents != 4 {
		t.Fatalf("first page %v %+v", err, page)
	}
	last, _ := ix.Entities(ctx, allowed, FP, knowledge.EntityQuery{Limit: 5, Offset: 10})
	if len(last.Entities) != 3 || last.Next != 0 {
		t.Fatalf("last page %+v", last)
	}
	card, _ := ix.Entity(ctx, allowed, FP, knowledge.EntityID("service:billing"), 2)
	if !card.Truncated || len(card.MentionList) != 2 || len(card.DocumentList) != 2 || card.EntitySummary.Mentions != 4 {
		t.Fatalf("card limit %+v", card)
	}
	v, _ := ix.Article(ctx, allowed, FP, Key(docs[0]), 2)
	if !v.Truncated || len(v.Entities) != 2 || len(v.Mentions) != 2 {
		t.Fatalf("article limit %+v", v)
	}
	if _, err = ix.Entity(ctx, knowledge.AllowedSet{}, FP, knowledge.EntityID("service:billing"), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("empty allowed set reads", err)
	}
	empty, err := ix.Entities(ctx, knowledge.AllowedSet{}, FP, knowledge.EntityQuery{})
	if err != nil || empty.Total != 0 || empty.Entities == nil {
		t.Fatalf("empty allowed set %v %+v", err, empty)
	}
}
