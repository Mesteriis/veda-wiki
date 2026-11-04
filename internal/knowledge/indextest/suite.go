package indextest

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Run runs the contract suite; newIndex is called once per case.
func Run(t *testing.T, newIndex Factory) {
	cases := []struct {
		name string
		run  func(*testing.T, Factory)
	}{
		{"HiddenDocumentDoesNotInfluencePublicReads", hiddenDocument},
		{"StaleRevisionPublishedLateIsNotRead", staleRevision},
		{"PublishedAndCurrentRevisionsDiffer", publishedAndCurrent},
		{"RelatedArticlesExplainAndSkipGenericEntities", relatedArticles},
		{"GraphBoundsAndFilters", graphBounds},
		{"ContextSelectsWithReasonsWithinBytes", contextSelects},
		{"PublishReplacesAndRetireKeepsSharedEntities", publishAndRetire},
		{"PagingAndLimits", paging},
		{"ClaimEndWithoutMentionsAddsNoNode", claimEndWithoutMentions},
		{"HiddenClaimDoesNotBridgePublicEntities", hiddenBridge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newIndex) })
	}
}

// Parity publishes the fixtures to ref and ix and requires every read to
// be identical for the public, reader and owner allowed sets.
func Parity(t *testing.T, ref, ix knowledge.Index) {
	t.Helper()
	docs := []Doc{PubA, PubB, Hidden, OrderA, OrderB}
	docs = append(docs, genericDocs()...)
	db := Database(docs...)
	Publish(t, ref, docs...)
	Publish(t, ix, docs...)
	for _, d := range []Doc{OrderA, OrderB} {
		for _, index := range []knowledge.Index{ref, ix} {
			if err := index.Publish(ctx, knowledge.Snapshot{Key: Key(d), Fingerprint: FP}, orderedGraph(d)); err != nil {
				t.Fatal(err)
			}
		}
	}
	sets := map[string]knowledge.AllowedSet{
		"public": Public(db),
		"reader": knowledge.Allowed(db, false, knowledge.Scope{}, now),
		"owner":  Owner(db),
	}
	extra := []knowledge.SnapshotKey{Key(Hidden), Key(genericDocs()[0]), Key(genericDocs()[6]), Key(OrderA), Key(OrderB)}
	for name, allowed := range sets {
		want, got := Reads(ref, allowed, extra...), Reads(ix, allowed, extra...)
		for i := range want {
			if !reflect.DeepEqual(want[i], got[i]) {
				t.Errorf("%s read %d differs:\nwant %+v\ngot  %+v", name, i, want[i], got[i])
			}
		}
	}
	for name, allowed := range sets {
		for _, limit := range []int{1, 2} {
			want, werr := ref.Entity(ctx, allowed, FP, OrderedID, limit)
			got, gerr := ix.Entity(ctx, allowed, FP, OrderedID, limit)
			if werr != nil || want.Documents != 2 {
				t.Fatalf("%s: ordering fixture not readable: %v %+v", name, werr, want)
			}
			if !reflect.DeepEqual(want, got) || !errors.Is(gerr, werr) {
				t.Errorf("%s ordered entity (limit %d) differs:\nwant %+v %v\ngot  %+v %v", name, limit, want, werr, got, gerr)
			}
		}
	}
	for _, id := range []string{knowledge.EntityID("person@pub:иван"), knowledge.EntityID("technology@pub:api")} {
		want, werr := ref.Entity(ctx, Owner(db), FP, id, 0)
		got, gerr := ix.Entity(ctx, Owner(db), FP, id, 0)
		if !reflect.DeepEqual(want, got) || !errors.Is(gerr, werr) {
			t.Errorf("entity %s differs:\nwant %+v %v\ngot  %+v %v", id, want, werr, got, gerr)
		}
	}
}

// Review Focus 1: publishing a hidden document changes no answer a public
// reader gets: names, aliases, counts, ranking, claims, related articles,
// traversal and context are all identical.
func hiddenDocument(t *testing.T, newIndex Factory) {
	public := Public(Database(PubA, PubB, Hidden))
	if public.Has(Key(Hidden)) || !public.Has(Key(PubA)) {
		t.Fatal("fixture allowed set")
	}
	without := newIndex(t)
	Publish(t, without, PubA, PubB)
	ix := newIndex(t)
	Publish(t, ix, PubA, PubB, Hidden)
	if w, g := Reads(without, public), Reads(ix, public); !reflect.DeepEqual(g, w) {
		t.Fatalf("hidden document changed public reads:\n%+v\n%+v", g, w)
	}
	card, err := ix.Entity(ctx, public, FP, knowledge.EntityID("service:billing"), 0)
	if err != nil || card.Name != "billing" || len(card.Aliases) != 0 || card.Documents != 2 || card.EntitySummary.Mentions != 2 {
		t.Fatalf("public card %v %+v", err, card)
	}
	if _, err = ix.Entity(ctx, public, FP, knowledge.EntityID("technology@priv:secretdb"), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("hidden-only entity readable", err)
	}
	if _, err = ix.Article(ctx, public, FP, Key(Hidden), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("hidden article readable", err)
	}
	if _, err = ix.Graph(ctx, public, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{Snapshot: ptr(Key(Hidden))}}); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("hidden graph center readable", err)
	}
	owner := Owner(Database(PubA, PubB, Hidden))
	ownerCard, err := ix.Entity(ctx, owner, FP, knowledge.EntityID("service:billing"), 0)
	if err != nil || ownerCard.Documents != 3 || !reflect.DeepEqual(ownerCard.Aliases, []string{"BILLING", "Billing"}) {
		t.Fatalf("owner card %v %+v", err, ownerCard)
	}
	if _, err = ix.Entity(ctx, owner, "kg1-other", knowledge.EntityID("service:billing"), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("other generation readable")
	}
}

// Review Focus 2: a slow job publishes the old revision after the fresh
// one; readers of the current text never see the old claims or entities.
func staleRevision(t *testing.T, newIndex Factory) {
	oldDoc := Doc{"pub", "a.md", "old text", []string{"service:billing", "technology:OldDB"}, [][3]string{{"billing", "depends_on", "OldDB"}}}
	newDoc := Doc{"pub", "a.md", "new text", []string{"service:billing", "technology:Redis"}, [][3]string{{"billing", "depends_on", "Redis"}}}
	ix := newIndex(t)
	Publish(t, ix, newDoc, oldDoc)
	db := domain.Database{Sources: Database().Sources, Articles: map[string]domain.Article{
		"pub/a.md": {SourceID: "pub", Path: "a.md", Versions: []domain.Revision{revision(1, oldDoc), revision(2, newDoc)}},
	}}
	for name, allowed := range map[string]knowledge.AllowedSet{"public": Public(db), "owner": Owner(db)} {
		if allowed.Has(Key(oldDoc)) {
			t.Fatalf("%s allowed the old revision", name)
		}
		v, err := ix.Article(ctx, allowed, FP, Key(newDoc), 0)
		if err != nil || len(v.Claims) != 1 || v.Claims[0].Evidence.Quote != "billing depends_on Redis" {
			t.Fatalf("%s article %v %+v", name, err, v)
		}
		card, err := ix.Entity(ctx, allowed, FP, knowledge.EntityID("service:billing"), 0)
		if err != nil || len(card.Claims) != 1 || card.Documents != 1 || card.Claims[0].Evidence.SHA != Key(newDoc).SHA {
			t.Fatalf("%s card %v %+v", name, err, card)
		}
		page, _ := ix.Entities(ctx, allowed, FP, knowledge.EntityQuery{Text: "olddb"})
		if page.Total != 0 {
			t.Fatalf("%s sees the old entity %+v", name, page)
		}
		if _, err = ix.Article(ctx, allowed, FP, Key(oldDoc), 0); !errors.Is(err, knowledge.ErrNotFound) {
			t.Fatalf("%s reads the old revision", name)
		}
		g, _ := ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: knowledge.EntityID("service:billing")}, Depth: 2})
		for _, n := range g.Nodes {
			if n.Label == "OldDB" || n.Key != nil && n.Key.SHA == Key(oldDoc).SHA {
				t.Fatalf("%s graph reaches the old revision: %+v", name, n)
			}
		}
	}
	if err := ix.Retire(ctx, []knowledge.Snapshot{{Key: Key(oldDoc), Fingerprint: FP}}); err != nil {
		t.Fatal(err)
	}
	snaps, err := ix.Snapshots(ctx)
	if err != nil || !reflect.DeepEqual(snaps, []knowledge.Snapshot{{Key: Key(newDoc), Fingerprint: FP}}) {
		t.Fatalf("snapshots after retire %v %+v", err, snaps)
	}
}

// Review Focus 3: the published and the current revision of one GitHub
// article give the public and the owner different graphs.
func publishedAndCurrent(t *testing.T, newIndex Factory) {
	pub := Doc{"gh", "page.md", "published text", []string{"service:billing", "host:db1"}, [][3]string{{"billing", "runs_on", "db1"}}}
	draft := Doc{"gh", "page.md", "draft text", []string{"service:billing", "host:db2"}, [][3]string{{"billing", "runs_on", "db2"}}}
	ix := newIndex(t)
	Publish(t, ix, pub, draft)
	db := Database()
	published := revision(3, pub)
	db.Articles["gh/page.md"] = domain.Article{SourceID: "gh", Path: "page.md", Versions: []domain.Revision{revision(4, draft)}, Published: &published}
	public, owner := Public(db), Owner(db)
	if !public.Has(Key(pub)) || public.Has(Key(draft)) || !owner.Has(Key(draft)) || !owner.Has(Key(pub)) {
		t.Fatalf("fixture allowed sets %v %v", public.Keys(), owner.Keys())
	}
	billing := knowledge.EntityID("service:billing")
	hosts := func(allowed knowledge.AllowedSet) []string {
		g, err := ix.Graph(ctx, allowed, FP, knowledge.GraphQuery{Center: knowledge.GraphCenter{EntityID: billing}})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, n := range g.Nodes {
			if n.Type == "host" {
				out = append(out, n.Label)
			}
		}
		return out
	}
	if got := hosts(public); !reflect.DeepEqual(got, []string{"db1"}) {
		t.Fatalf("public graph hosts %v", got)
	}
	if got := hosts(owner); len(got) != 2 {
		t.Fatalf("owner graph hosts %v", got)
	}
	if _, err := ix.Article(ctx, public, FP, Key(draft), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("public reads the draft")
	}
	v, err := ix.Article(ctx, owner, FP, Key(draft), 0)
	if err != nil || len(v.Claims) != 1 || v.Claims[0].Evidence.Quote != "billing runs_on db2" || len(v.Related) != 0 {
		t.Fatalf("owner draft %v %+v", err, v)
	}
	card, err := ix.Entity(ctx, public, FP, billing, 0)
	if err != nil || len(card.Claims) != 1 || card.Documents != 1 {
		t.Fatalf("public card %v %+v", err, card)
	}
}

func genericDocs() []Doc {
	var docs []Doc
	for i := range 6 {
		docs = append(docs, Doc{"pub", fmt.Sprintf("d%d.md", i), fmt.Sprintf("d%d", i), []string{"technology:API"}, nil})
	}
	docs[0].Names = append(docs[0].Names, "service:ledger", "technology:PostgreSQL", "person:Иван")
	docs[0].Claims = [][3]string{{"ledger", "uses", "PostgreSQL"}}
	docs[1].Names = append(docs[1].Names, "technology:PostgreSQL")
	docs[2].Names = append(docs[2].Names, "service:ledger")
	return append(docs, Doc{"pub2", "x.md", "x", []string{"person:Иван"}, nil})
}

func relatedArticles(t *testing.T, newIndex Factory) {
	docs := genericDocs()
	ix := newIndex(t)
	Publish(t, ix, docs...)
	allowed := Public(Database(docs...))
	v, err := ix.Article(ctx, allowed, FP, Key(docs[0]), 0)
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
	card, _ := ix.Entity(ctx, allowed, FP, knowledge.EntityID("person@pub:иван"), 0)
	if len(card.Candidates) != 1 || card.Candidates[0].Resolution.Method != knowledge.ResolveCandidate {
		t.Fatalf("candidates %+v", card.Candidates)
	}
	scoped := Public(Database(docs[:6]...))
	if card, _ = ix.Entity(ctx, scoped, FP, knowledge.EntityID("person@pub:иван"), 0); len(card.Candidates) != 0 {
		t.Fatal("candidate from a document outside the allowed set")
	}
}
