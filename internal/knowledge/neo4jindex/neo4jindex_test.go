package neo4jindex

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/indextest"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
)

// Integration tests run against a real Neo4j when VEDA_TEST_NEO4J_URI is
// set, with VEDA_TEST_NEO4J_USER (default neo4j) and the password in the
// file VEDA_TEST_NEO4J_PASSWORD_FILE. Every case deletes all VEDA knowledge
// nodes first: point it at a disposable database, for example the one of
// scripts/knowledge_stack.py.
func testIndex(t *testing.T) *Index {
	t.Helper()
	uri := os.Getenv("VEDA_TEST_NEO4J_URI")
	if uri == "" {
		t.Skip("set VEDA_TEST_NEO4J_URI (and VEDA_TEST_NEO4J_PASSWORD_FILE) to run the Neo4j index tests")
	}
	user := os.Getenv("VEDA_TEST_NEO4J_USER")
	if user == "" {
		user = "neo4j"
	}
	data, err := os.ReadFile(os.Getenv("VEDA_TEST_NEO4J_PASSWORD_FILE"))
	if err != nil {
		t.Fatal("VEDA_TEST_NEO4J_PASSWORD_FILE is not readable")
	}
	ix, err := New(Config{URI: uri, User: user, Password: strings.TrimSpace(string(data))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ix.Close(context.Background()) })
	if err = ix.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	wipe(t, ix)
	t.Cleanup(func() { wipe(t, ix) })
	return ix
}

func wipe(t *testing.T, ix *Index) {
	t.Helper()
	err := ix.write(context.Background(), ix.publish, func(tx neo4j.ManagedTransaction) error {
		_, err := collect(context.Background(), tx, `MATCH (n) WHERE n:Snapshot OR n:Evidence OR n:Mention OR n:Claim OR n:Entity DETACH DELETE n`, nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func counts(t *testing.T, ix *Index) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := ix.read(context.Background(), func(tx neo4j.ManagedTransaction) error {
		recs, err := collect(context.Background(), tx, `MATCH (n) WHERE n:Snapshot OR n:Evidence OR n:Mention OR n:Claim OR n:Entity RETURN labels(n)[0], count(*)`, nil)
		for _, r := range recs {
			out[str(r.Values[0])] = integer(r.Values[1])
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIndexContract(t *testing.T) {
	indextest.Run(t, func(t *testing.T) knowledge.Index { return testIndex(t) })
}

func TestParityWithMemindex(t *testing.T) {
	indextest.Parity(t, memindex.New(), testIndex(t))
}

func TestRepublishCreatesNoDuplicatesAndRetireRemovesOrphans(t *testing.T) {
	ix := testIndex(t)
	ctx := context.Background()
	indextest.Publish(t, ix, indextest.PubA, indextest.PubB)
	before := counts(t, ix)
	indextest.Publish(t, ix, indextest.PubA, indextest.PubB)
	after := counts(t, ix)
	want := map[string]int64{"Snapshot": 2, "Entity": 3, "Mention": 5, "Claim": 2, "Evidence": 7}
	for label, n := range want {
		if before[label] != n || after[label] != n {
			t.Fatalf("%s: %d then %d, want %d", label, before[label], after[label], n)
		}
	}
	snapA := knowledge.Snapshot{Key: indextest.Key(indextest.PubA), Fingerprint: indextest.FP}
	if err := ix.Retire(ctx, []knowledge.Snapshot{snapA}); err != nil {
		t.Fatal(err)
	}
	if c := counts(t, ix); c["Entity"] != 3 || c["Snapshot"] != 1 || c["Mention"] != 3 {
		t.Fatalf("retire removed shared entities: %v", c)
	}
	if err := ix.Retire(ctx, []knowledge.Snapshot{{Key: indextest.Key(indextest.PubB), Fingerprint: indextest.FP}}); err != nil {
		t.Fatal(err)
	}
	if c := counts(t, ix); len(c) != 0 {
		t.Fatalf("orphans left: %v", c)
	}
}

// An entity listed in a graph that no mention or claim refers to is not
// stored, so it can never become a permanent orphan.
func TestUnreferencedEntitiesAreNotStored(t *testing.T) {
	ix := testIndex(t)
	ctx := context.Background()
	g := indextest.Graph(indextest.PubA, indextest.FP)
	g.Entities = append(g.Entities, knowledge.GraphEntity{ID: knowledge.EntityID("service:unused"), Type: "service", Key: "service:unused"})
	snap := knowledge.Snapshot{Key: indextest.Key(indextest.PubA), Fingerprint: indextest.FP}
	if err := ix.Publish(ctx, snap, g); err != nil {
		t.Fatal(err)
	}
	if c := counts(t, ix); c["Entity"] != 2 {
		t.Fatalf("entities %v", c)
	}
	if err := ix.Retire(ctx, []knowledge.Snapshot{snap}); err != nil {
		t.Fatal(err)
	}
	if c := counts(t, ix); len(c) != 0 {
		t.Fatalf("left after retire: %v", c)
	}
}

// A snapshot left incomplete (never possible after a committed publish, but
// a reader must not depend on that) is invisible to every read.
func TestIncompleteSnapshotIsNeverRead(t *testing.T) {
	ix := testIndex(t)
	ctx := context.Background()
	indextest.Publish(t, ix, indextest.PubA)
	sid := SnapshotID(indextest.FP, indextest.Key(indextest.PubA))
	err := ix.write(ctx, ix.publish, func(tx neo4j.ManagedTransaction) error {
		_, err := collect(ctx, tx, `MATCH (s:Snapshot {id: $sid}) SET s.complete = false`, map[string]any{"sid": sid})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed := indextest.Owner(indextest.Database(indextest.PubA))
	if _, err = ix.Article(ctx, allowed, indextest.FP, indextest.Key(indextest.PubA), 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("incomplete snapshot readable", err)
	}
	page, err := ix.Entities(ctx, allowed, indextest.FP, knowledge.EntityQuery{})
	if err != nil || page.Total != 0 {
		t.Fatalf("incomplete snapshot entities %v %+v", err, page)
	}
	if snaps, _ := ix.Snapshots(ctx); len(snaps) != 0 {
		t.Fatalf("incomplete snapshot listed %+v", snaps)
	}
}

func TestPublishRejectsInvalidGraphsBeforeWriting(t *testing.T) {
	// No server: validation fails before any connection is made.
	ix, err := New(Config{URI: "bolt://127.0.0.1:1", User: "neo4j", Password: "not-a-real-password", ConnectTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close(context.Background())
	snap := knowledge.Snapshot{Key: indextest.Key(indextest.PubB), Fingerprint: indextest.FP}
	mutations := map[string]func(*knowledge.Graph){
		"undeclared relation":  func(g *knowledge.Graph) { g.Claims[0].Relation = "owns`]->() DETACH DELETE (n" },
		"relation end type":    func(g *knowledge.Graph) { g.Claims[0].Relation = "uses" },
		"undeclared entity":    func(g *knowledge.Graph) { g.Entities[0].Type = "planet" },
		"foreign evidence":     func(g *knowledge.Graph) { g.Mentions[0].Evidence.SHA = "other" },
		"other fingerprint":    func(g *knowledge.Graph) { g.Claims[0].Evidence.Fingerprint = "kg1-other" },
		"unknown claim status": func(g *knowledge.Graph) { g.Claims[0].Status = "proven" },
		"entity outside graph": func(g *knowledge.Graph) { g.Mentions[0].EntityID = "ent-missing" },
	}
	for name, mutate := range mutations {
		g := indextest.Graph(indextest.PubB, indextest.FP)
		mutate(&g)
		var invalid invalidGraph
		if err := ix.Publish(context.Background(), snap, g); !errors.As(err, &invalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestUnreachableServerIsUnavailableWithoutSecrets(t *testing.T) {
	const password = "s3cret-password-value"
	ix, err := New(Config{URI: "bolt://127.0.0.1:1", User: "neo4j", Password: password, ConnectTimeout: 200 * time.Millisecond, QueryTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close(context.Background())
	start := time.Now()
	err = ix.Status(context.Background())
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), password) {
		t.Fatalf("status: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("unbounded connect: %v", time.Since(start))
	}
	allowed := indextest.Owner(indextest.Database(indextest.PubA))
	if _, err = ix.Entities(context.Background(), allowed, indextest.FP, knowledge.EntityQuery{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("read: %v", err)
	}
	if _, err = New(Config{URI: "bolt://127.0.0.1:7687", User: "neo4j"}); err == nil {
		t.Fatal("missing password accepted")
	}
}
