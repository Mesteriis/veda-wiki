package knowledge_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/jobs"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// Brief §12 case 3: repeated reconciliation, requeue and replay of the same
// job create no duplicates and do not call the extractor again.
func TestReplayCreatesNoDuplicates(t *testing.T) {
	h := newHarness(t, 1)
	key := h.write("pub", "billing.md", "billing runs on db1 and uses Redis.\n")
	h.start()
	h.settle()
	first, err := h.article(key)
	if err != nil || !reflect.DeepEqual(relations(first), []string{"runs_on", "uses"}) || len(first.Mentions) != 3 {
		t.Fatalf("first index: %v %+v", err, first)
	}
	calls := h.ex.callCount()
	for range 3 {
		if _, err = h.svc.Reindex(context.Background(), "pub", "billing.md"); err != nil {
			t.Fatal(err)
		}
		h.settle()
	}
	again, _ := h.article(key)
	if !reflect.DeepEqual(again, first) {
		t.Fatalf("replay changed the graph:\n%+v\n%+v", again, first)
	}
	if h.ex.callCount() != calls {
		t.Fatal("replay called the extractor again instead of reusing the stored result")
	}
	if jobs := h.jobsOf(key); len(jobs) != 1 || jobs[0].State != knowledge.JobSucceeded {
		t.Fatalf("manifest rows %+v", jobs)
	}
}

// Brief §12 case 4 and Review Focus 2 (part): a new revision supersedes the
// old snapshot and its claims leave the index.
func TestNewRevisionRetiresOldClaims(t *testing.T) {
	h := newHarness(t, 1)
	old := h.write("pub", "billing.md", "billing runs on db1.\n")
	h.start()
	h.settle()
	fresh := h.write("pub", "billing.md", "billing uses Redis.\n")
	h.settle()
	if _, err := h.article(old); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("old snapshot still readable", err)
	}
	v, err := h.article(fresh)
	if err != nil || !reflect.DeepEqual(relations(v), []string{"uses"}) {
		t.Fatalf("fresh %v %+v", err, v)
	}
	if got := h.published(); len(got) != 1 || got[0].Key != fresh {
		t.Fatalf("index holds %+v", got)
	}
	if j := h.jobsOf(old); len(j) != 1 || j[0].State != knowledge.JobSuperseded {
		t.Fatalf("old job %+v", j)
	}
}

// recordingManifest reports every Finish call.
type recordingManifest struct {
	*jobs.DB
	finished chan knowledge.Snapshot
}

func (m recordingManifest) Finish(ctx context.Context, s knowledge.Snapshot, f knowledge.Finish, now time.Time) (bool, error) {
	ok, err := m.DB.Finish(ctx, s, f, now)
	m.finished <- s
	return ok, err
}

// Review Focus 2: a slow worker extracting an old SHA finishes after a
// fresh revision was published; it stores its result but never publishes
// it, and its manifest row stays superseded.
func TestSlowStaleWorkerCannotPublishOverFresh(t *testing.T) {
	h := newHarness(t, 2)
	rec := recordingManifest{DB: h.db, finished: make(chan knowledge.Snapshot, 64)}
	cfg := h.config(2, knowledge.SchemaV1(), nil, "")
	cfg.Manifest = rec
	svc, err := knowledge.NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	release := h.ex.gate("SLOW")
	stale := h.write("pub", "billing.md", "SLOW billing runs on db1.\n")
	h.start()
	<-h.ex.entered // the stale job is inside the extractor
	fresh := h.write("pub", "billing.md", "billing uses Redis.\n")
	h.settle() // supersedes the stale row; the second worker publishes fresh
	if got := h.published(); len(got) != 1 || got[0].Key != fresh {
		t.Fatalf("index before release %+v", got)
	}
	close(release)
	for s := range rec.finished {
		if s.Key == stale {
			break
		}
	}
	if got := h.published(); len(got) != 1 || got[0].Key != fresh {
		t.Fatalf("stale worker published: %+v", got)
	}
	if j := h.jobsOf(stale); len(j) != 1 || j[0].State != knowledge.JobSuperseded {
		t.Fatalf("stale row %+v", j)
	}
	if _, found, _ := h.db.Result(context.Background(), knowledge.Snapshot{Key: stale, Fingerprint: testPipeline.Fingerprint}, testPipeline.Extraction); !found {
		t.Fatal("stale result was not kept")
	}
	v, _ := h.article(fresh)
	if !reflect.DeepEqual(relations(v), []string{"uses"}) {
		t.Fatalf("reader sees %v", relations(v))
	}
}

// Brief §12 case 3: a fingerprint change reprocesses everything, retires
// the old generation and reads never mix generations. An alias change
// reuses stored extractions; a schema change extracts again.
func TestFingerprintChangeReprocessesWithoutMixing(t *testing.T) {
	h := newHarness(t, 1)
	key := h.write("pub", "ledger.md", "ledger uses PostgreSQL.\n")
	h.start()
	h.settle()
	h.stop()
	calls := h.ex.callCount()
	aliases, digest, err := knowledge.ParseAliases([]byte(`{"version":1,"aliases":{"technology":{"PostgreSQL":"Postgres"}}}`), knowledge.SchemaV1())
	if err != nil {
		t.Fatal(err)
	}
	p2 := pipeline(knowledge.SchemaV1(), aliases, digest, extractorSettings)
	h.svc = h.service(1, knowledge.SchemaV1(), aliases, digest)
	h.start()
	h.settle()
	for _, s := range h.published() {
		if s.Fingerprint != p2.Fingerprint {
			t.Fatalf("old generation still published: %+v", s)
		}
	}
	if h.ex.callCount() != calls {
		t.Fatal("alias change called the extractor")
	}
	ctx := context.Background()
	if _, err = h.ix.Article(ctx, h.allowed(false), testPipeline.Fingerprint, key, 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("old fingerprint still readable", err)
	}
	v, err := h.ix.Article(ctx, h.allowed(false), p2.Fingerprint, key, 0)
	if err != nil || v.Claims[0].TailID != knowledge.EntityID("technology:postgres") || v.Claims[0].Evidence.Fingerprint != p2.Fingerprint {
		t.Fatalf("new generation %v %+v", err, v)
	}
	h.stop()
	schema := knowledge.SchemaV1()
	schema.Digest = "sha256:other"
	h.svc = h.service(1, schema, nil, "")
	h.start()
	h.settle()
	if h.ex.callCount() != calls+1 {
		t.Fatal("schema change did not extract again", h.ex.callCount(), calls)
	}
}

// Brief §12 case 5: deleting or moving an article retires its evidence; an
// entity shared with another current document stays.
func TestDeleteAndMoveRetireEvidenceKeepSharedEntity(t *testing.T) {
	h := newHarness(t, 1)
	billing := h.write("pub", "billing.md", "billing uses Redis.\n")
	ledger := h.write("pub", "ledger.md", "ledger caches in Redis.\n")
	h.start()
	h.settle()
	redis := knowledge.EntityID("technology@pub:redis")
	ctx := context.Background()
	card, err := h.ix.Entity(ctx, h.allowed(false), testPipeline.Fingerprint, redis, 0)
	if err != nil || card.Documents != 2 {
		t.Fatalf("before %v %+v", err, card)
	}
	h.remove("pub", "billing.md")
	h.settle()
	if _, err = h.article(billing); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("deleted article still readable")
	}
	card, err = h.ix.Entity(ctx, h.allowed(false), testPipeline.Fingerprint, redis, 0)
	if err != nil || card.Documents != 1 || card.DocumentList[0] != ledger || len(card.Claims) != 0 {
		t.Fatalf("shared entity after delete %v %+v", err, card)
	}
	// Move: the article leaves its old path and appears at a new one.
	h.remove("pub", "ledger.md")
	moved := h.write("pub", "archive/ledger.md", "ledger caches in Redis.\n")
	h.settle()
	if got := h.published(); len(got) != 1 || got[0].Key != moved {
		t.Fatalf("after move %+v", got)
	}
	// A deleted flag works like removal.
	storetest.Mutate(t, h.st, func(db *domain.Database) {
		a := db.Articles["pub/archive/ledger.md"]
		a.Deleted = true
		db.Articles["pub/archive/ledger.md"] = a
	})
	h.settle()
	if got := h.published(); len(got) != 0 {
		t.Fatalf("deleted article indexed %+v", got)
	}
	if _, err = h.ix.Entity(ctx, h.allowed(false), testPipeline.Fingerprint, redis, 0); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatal("entity without evidence still readable")
	}
}

// publishFails is an index whose publications never complete.
type publishFails struct{ *memindex.Index }

func (publishFails) Publish(context.Context, knowledge.Snapshot, knowledge.Graph) error {
	return errors.New("connection reset")
}

// Brief §12 case 6: a commit without a running service, a job left running
// by a crash, a result stored but never published, and a lost index are
// all repaired by start-up reconciliation; nothing half-published is read.
func TestCrashRecoveryByReconciliation(t *testing.T) {
	h := newHarness(t, 1)
	key := h.write("pub", "billing.md", "billing runs on db1.\n") // no service: no signal
	cfg := h.config(1, knowledge.SchemaV1(), nil, "")
	cfg.Index = publishFails{h.ix}
	svc, err := knowledge.NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	h.start()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, found, _ := h.db.Result(context.Background(), knowledge.Snapshot{Key: key, Fingerprint: testPipeline.Fingerprint}, testPipeline.Extraction); found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("result never stored")
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.stop() // crash after extraction, before publication
	if len(h.published()) != 0 {
		t.Fatal("a failed publication is visible")
	}
	if _, err = h.db.Claim(context.Background(), testPipeline.Fingerprint, time.Now().Add(time.Hour), 10); err != nil {
		t.Fatal(err) // leave the row running, as a crash mid-job would
	}
	calls := h.ex.callCount()
	h.ix = memindex.New() // the index restarts empty
	h.svc = h.service(1, knowledge.SchemaV1(), nil, "")
	h.start()
	h.settle()
	if _, err := h.article(key); err != nil {
		t.Fatal("not recovered", err)
	}
	if h.ex.callCount() != calls {
		t.Fatal("recovery re-extracted a stored result")
	}
	h.stop()
	h.ix = memindex.New() // lost again after success
	h.svc = h.service(1, knowledge.SchemaV1(), nil, "")
	h.start()
	h.settle()
	if _, err := h.article(key); err != nil || h.ex.callCount() != calls {
		t.Fatal("lost index not republished from the stored result", err)
	}
}

// Brief §12 case 7 and Review Focus 5: extractor and index outages leave
// VEDA writes and reads working, report degraded without spending
// attempts, and indexing catches up after recovery.
func TestOutagesDegradeAndRecover(t *testing.T) {
	h := newHarness(t, 1)
	h.ex.setDown(true)
	h.start()
	key := h.write("pub", "billing.md", "billing runs on db1.\n")
	waitFor(t, func() bool {
		return slices.Contains(h.svc.Status(context.Background()).Reasons, knowledge.CodeExtractorUnavailable)
	})
	if err := h.st.View(func(db *domain.Database) error {
		if db.Articles["pub/billing.md"].Versions[0].SHA != key.SHA {
			return errors.New("read failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h.write("pub", "other.md", "plain text\n") // writes still commit
	st := h.svc.Status(context.Background())
	if st.State != knowledge.StateDegraded {
		t.Fatalf("status %+v", st)
	}
	for _, j := range h.jobsOf(key) {
		if j.Attempts > 1 || j.State == knowledge.JobFailed {
			t.Fatalf("outage spent attempts: %+v", j)
		}
	}
	h.ex.setDown(false)
	h.settle()
	if _, err := h.article(key); err != nil {
		t.Fatal("did not catch up", err)
	}
	h.ix.SetUnavailable(errors.New("index down"))
	fresh := h.write("pub", "billing.md", "billing uses Redis.\n")
	waitFor(t, func() bool {
		return slices.Contains(h.svc.Status(context.Background()).Reasons, knowledge.CodeIndexUnavailable)
	})
	h.ix.SetUnavailable(nil)
	if st = h.settle(); st.State != knowledge.StateReady || len(st.Reasons) != 0 {
		t.Fatalf("after index recovery %+v", st)
	}
	if _, err := h.article(fresh); err != nil {
		t.Fatal(err)
	}
	for _, line := range h.logs {
		if strings.Contains(line, "billing") || strings.Contains(line, "http") {
			t.Fatalf("log leaks data: %q", line)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// The commit signal never blocks the store, even when every worker is
// stuck inside the extractor and the queue is full.
func TestStuckWorkerDoesNotBlockStoreUpdates(t *testing.T) {
	h := newHarness(t, 1)
	release := h.ex.gate("STUCK")
	defer close(release)
	h.write("pub", "a.md", "STUCK billing\n")
	h.write("pub", "b.md", "STUCK ledger\n")
	h.start()
	<-h.ex.entered
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 300 {
			storetest.Mutate(t, h.st, func(db *domain.Database) {
				text := strings.Repeat("x", i+1)
				db.Articles["pub/many.md"] = domain.Article{SourceID: "pub", Path: "many.md", Versions: []domain.Revision{{Number: 1, Text: text, SHA: digest(text)}}}
			})
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("store updates blocked behind the knowledge worker")
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() { defer wg.Done(); h.svc.Signal() }()
	}
	wg.Wait()
}

// ErrBlockTooLarge: a block over the extractor limit fails at once with
// too_large and is not retried.
func TestTooLargeFailsWithoutRetry(t *testing.T) {
	h := newHarness(t, 1)
	key := h.write("pub", "huge.md", strings.Repeat("слово ", knowledge.MaxBlockRunes/6+10)+"\n")
	h.start()
	h.settle()
	j := h.jobsOf(key)
	if len(j) != 1 || j[0].State != knowledge.JobFailed || j[0].LastError != knowledge.CodeTooLarge || j[0].Attempts != 1 || h.ex.callCount() != 0 {
		t.Fatalf("job %+v calls %d", j, h.ex.callCount())
	}
}

// The current revision and a differing publication copy are both indexed,
// each with its own graph.
func TestCurrentAndPublishedSnapshotsBothIndexed(t *testing.T) {
	h := newHarness(t, 1)
	published, draft := "billing runs on db1.\n", "billing uses Redis.\n"
	storetest.Mutate(t, h.st, func(db *domain.Database) {
		db.Sources["gh"] = domain.Source{ID: "gh", Title: "gh", Kind: "wiki", Visibility: "public", ConnectionID: "c1"}
		pub := domain.Revision{Number: 1, Text: published, SHA: digest(published)}
		db.Articles["gh/page.md"] = domain.Article{SourceID: "gh", Path: "page.md", Versions: []domain.Revision{pub, {Number: 2, Text: draft, SHA: digest(draft)}}, Published: &pub}
	})
	h.start()
	h.settle()
	pk := knowledge.SnapshotKey{SourceID: "gh", Path: "page.md", SHA: digest(published)}
	dk := knowledge.SnapshotKey{SourceID: "gh", Path: "page.md", SHA: digest(draft)}
	a, errA := h.article(pk)
	b, errB := h.article(dk)
	if errA != nil || errB != nil || !reflect.DeepEqual(relations(a), []string{"runs_on"}) || !reflect.DeepEqual(relations(b), []string{"uses"}) {
		t.Fatalf("published %v %v / current %v %v", relations(a), errA, relations(b), errB)
	}
	if v, _ := h.article(pk); len(v.Related) != 0 {
		t.Fatal("two copies of one article are reported as related articles")
	}
}

// The extractor is reconfigured while a job runs: the response reports other
// settings, so it is discarded; the service reloads the settings, switches
// the fingerprint and reprocesses everything; reads never mix generations.
func TestExtractorReconfigurationSwitchesFingerprint(t *testing.T) {
	h := newHarness(t, 1)
	settled := h.write("pub", "ledger.md", "ledger uses PostgreSQL.\n")
	h.start()
	h.settle()
	release := h.ex.gate("SLOW")
	slow := h.write("pub", "billing.md", "SLOW billing uses Redis.\n")
	<-h.ex.entered
	greedy := json.RawMessage(strings.Replace(string(extractorSettings), `"beam"`, `"greedy"`, 1))
	h.ex.reconfigure(greedy)
	close(release)
	newFP := pipeline(knowledge.SchemaV1(), nil, "", greedy).Fingerprint
	waitFor(t, func() bool { p, ok := h.svc.Pipeline(); return ok && p.Fingerprint == newFP })
	h.settle()
	for _, s := range h.published() {
		if s.Fingerprint != newFP {
			t.Fatalf("old generation published: %+v", s)
		}
	}
	if _, found, _ := h.db.Result(context.Background(), knowledge.Snapshot{Key: slow, Fingerprint: testPipeline.Fingerprint}, testPipeline.Extraction); found {
		t.Fatal("a result with other settings was stored under the old fingerprint")
	}
	for _, k := range []knowledge.SnapshotKey{settled, slow} {
		if _, err := h.article(k); err != nil {
			t.Fatal("not reprocessed under the new fingerprint", k.Path, err)
		}
	}
	raw, ok, _ := h.db.ExtractorPipeline(context.Background())
	if !ok || !strings.Contains(string(raw), "greedy") {
		t.Fatal("settings not recorded")
	}
	// A restart with the extractor down keeps the recorded fingerprint.
	h.stop()
	h.ex.setDown(true)
	h.svc = h.service(1, knowledge.SchemaV1(), nil, "")
	h.start()
	waitFor(t, func() bool { p, ok := h.svc.Pipeline(); return ok && p.Fingerprint == newFP })
}
