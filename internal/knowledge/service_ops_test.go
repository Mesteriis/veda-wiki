package knowledge_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/jobs"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
)

// Reindex covers one article, one source or everything; without force it
// rebuilds graphs from stored results, with force it extracts again. One
// operation runs at a time.
func TestReindexScopesForceAndExclusion(t *testing.T) {
	h := newHarness(t, 1)
	h.write("pub", "a.md", "billing uses Redis.\n")
	h.write("pub", "b.md", "ledger uses PostgreSQL.\n")
	h.write("priv", "c.md", "billing runs on db1.\n")
	h.start()
	h.settle()
	ctx := context.Background()
	cases := []struct {
		scope            knowledge.ReindexScope
		requeued, extras int
	}{
		{knowledge.ReindexScope{SourceID: "pub", Path: "a.md"}, 1, 0},
		{knowledge.ReindexScope{SourceID: "pub"}, 2, 0},
		{knowledge.ReindexScope{}, 3, 0},
		{knowledge.ReindexScope{SourceID: "pub", Path: "a.md", Force: true}, 1, 1},
		{knowledge.ReindexScope{SourceID: "priv", Force: true}, 1, 1},
		{knowledge.ReindexScope{Force: true}, 3, 3},
	}
	for _, tc := range cases {
		calls := h.ex.callCount()
		op, err := h.svc.Reindex(ctx, tc.scope)
		if err != nil || op.ID == "" || op.Requeued != tc.requeued {
			t.Fatalf("%+v: %+v %v", tc.scope, op, err)
		}
		st := h.settle()
		if got := h.ex.callCount() - calls; got != tc.extras {
			t.Fatalf("%+v: %d extractor calls, want %d", tc.scope, got, tc.extras)
		}
		if st.LastReindex == nil || st.LastReindex.ID != op.ID {
			t.Fatalf("status does not report the operation: %+v", st.LastReindex)
		}
	}
	if len(h.published()) != 3 {
		t.Fatal("reindex lost graphs", h.published())
	}
	if _, err := h.svc.Reindex(ctx, knowledge.ReindexScope{Path: "a.md"}); err == nil {
		t.Fatal("path without source accepted")
	}

	blocking := &blockingReindex{DB: h.db, entered: make(chan struct{}), release: make(chan struct{})}
	cfg := h.config(1, knowledge.SchemaV1(), nil, "")
	cfg.Manifest = blocking
	h.stop()
	h.svc, _ = knowledge.NewService(cfg)
	h.start()
	h.settle()
	done := make(chan error, 1)
	go func() { _, err := h.svc.Reindex(ctx, knowledge.ReindexScope{}); done <- err }()
	<-blocking.entered
	if _, err := h.svc.Reindex(ctx, knowledge.ReindexScope{SourceID: "pub"}); !errors.Is(err, knowledge.ErrReindexBusy) {
		t.Fatal("second operation admitted", err)
	}
	close(blocking.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type blockingReindex struct {
	*jobs.DB
	entered, release chan struct{}
}

func (b *blockingReindex) Reindex(ctx context.Context, fp knowledge.Fingerprint, source, path string, force bool, now time.Time) (int, error) {
	close(b.entered)
	<-b.release
	return b.DB.Reindex(ctx, fp, source, path, force, now)
}

// Distinct failure codes: timeouts, extractor failures and contract
// violations are told apart, and each spends an attempt.
func TestFailureCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{fmt.Errorf("%w: test", knowledge.ErrTimeout), knowledge.CodeTimeout},
		{fmt.Errorf("%w: status 500", knowledge.ErrExtractorError), knowledge.CodeExtractorError},
		{fmt.Errorf("%w: bad span", knowledge.ErrInvalidResponse), knowledge.CodeInvalidResult},
	}
	for _, tc := range cases {
		h := newHarness(t, 1)
		h.ex.failWith = tc.err
		key := h.write("pub", "a.md", "billing uses Redis.\n")
		h.start()
		waitFor(t, func() bool {
			j := h.jobsOf(key)
			return len(j) == 1 && j[0].LastError != ""
		})
		j := h.jobsOf(key)[0]
		if j.LastError != tc.code || j.State != knowledge.JobPending || j.Attempts != 1 || !j.NextAttempt.After(time.Now()) {
			t.Fatalf("%v: %+v", tc.err, j)
		}
		h.stop()
	}
}

// A busy extractor (its one inference slot taken) is retried without
// spending attempts and without degrading the module.
func TestBusyExtractorRetriesWithoutDegrading(t *testing.T) {
	h := newHarness(t, 2)
	h.ex.busy = 3
	key := h.write("pub", "a.md", "billing uses Redis.\n")
	h.start()
	if st := h.settle(); st.State != knowledge.StateReady || len(st.Reasons) != 0 {
		t.Fatalf("status %+v", st)
	}
	if _, err := h.article(key); err != nil {
		t.Fatal(err)
	}
	j := h.jobsOf(key)
	if len(j) != 1 || j[0].Attempts != 1 || j[0].LastError != "" || h.ex.callCount() != 4 {
		t.Fatalf("jobs %+v calls %d", j, h.ex.callCount())
	}
	h.logMu.Lock()
	defer h.logMu.Unlock()
	for _, line := range h.logs {
		if strings.Contains(line, "degraded") {
			t.Fatalf("busy degraded the module: %q", line)
		}
	}
}

type failingCounts struct {
	*jobs.DB
	fail bool
}

func (f *failingCounts) Counts(ctx context.Context, fp knowledge.Fingerprint) (map[knowledge.JobState]int, error) {
	if f.fail {
		return nil, errors.New("disk I/O error")
	}
	return f.DB.Counts(ctx, fp)
}

// A failed count read is reported in that status answer only; it does not
// degrade or pause the service.
func TestStatusHasNoSideEffects(t *testing.T) {
	h := newHarness(t, 1)
	counts := &failingCounts{DB: h.db, fail: true}
	cfg := h.config(1, knowledge.SchemaV1(), nil, "")
	cfg.Manifest = counts
	h.svc, _ = knowledge.NewService(cfg)
	h.start()
	waitFor(t, func() bool { _, ok := h.svc.Pipeline(); return ok })
	st := h.svc.Status(context.Background())
	if st.State != knowledge.StateDegraded || !slices.Contains(st.Reasons, knowledge.CodeManifestUnavailable) {
		t.Fatalf("failed read not reported: %+v", st)
	}
	counts.fail = false
	key := h.write("pub", "a.md", "billing uses Redis.\n")
	if st = h.settle(); len(st.Reasons) != 0 {
		t.Fatalf("status read left a reason: %+v", st)
	}
	if _, err := h.article(key); err != nil {
		t.Fatal("service paused by a status read", err)
	}
}

// pausingIndex lets a test act between the freshness check (done before
// Publish is called) and the publication, and before Publish returns.
type pausingIndex struct {
	*memindex.Index
	entered, proceed, published, release chan struct{}
}

func (p *pausingIndex) Publish(ctx context.Context, snap knowledge.Snapshot, g knowledge.Graph) error {
	if p.entered == nil {
		return p.Index.Publish(ctx, snap, g)
	}
	close(p.entered)
	<-p.proceed
	err := p.Index.Publish(ctx, snap, g)
	close(p.published)
	<-p.release
	p.entered = nil
	return err
}

// The View→Publish window: a commit lands after the worker checked the
// snapshot and before the graph is published. The stale graph reaches the
// index but no reader can see it (the allowed set names the new SHA), and
// the reconciliation triggered by the commit retires it.
func TestCommitBetweenCheckAndPublish(t *testing.T) {
	h := newHarness(t, 1)
	ix := &pausingIndex{Index: h.ix, entered: make(chan struct{}), proceed: make(chan struct{}), published: make(chan struct{}), release: make(chan struct{})}
	cfg := h.config(1, knowledge.SchemaV1(), nil, "")
	cfg.Index = ix
	h.svc, _ = knowledge.NewService(cfg)
	stale := h.write("pub", "a.md", "billing runs on db1.\n")
	h.start()
	<-ix.entered
	fresh := h.write("pub", "a.md", "ledger uses PostgreSQL.\n")
	close(ix.proceed)
	<-ix.published
	ctx := context.Background()
	fp := h.fp()
	for _, public := range []bool{true, false} {
		allowed := h.allowed(public)
		if _, err := h.ix.Article(ctx, allowed, fp, stale, 0); !errors.Is(err, knowledge.ErrNotFound) {
			t.Fatal("stale graph readable", public, err)
		}
		page, _ := h.ix.Entities(ctx, allowed, fp, knowledge.EntityQuery{})
		if page.Total != 0 {
			t.Fatalf("stale entities visible: %+v", page)
		}
	}
	if got := h.published(); len(got) != 1 || got[0].Key != stale {
		t.Fatalf("fixture: index holds %+v", got)
	}
	close(ix.release)
	h.settle()
	if got := h.published(); len(got) != 1 || got[0].Key != fresh {
		t.Fatalf("stale graph not retired: %+v", got)
	}
	if j := h.jobsOf(stale); len(j) != 1 || j[0].State != knowledge.JobSuperseded {
		t.Fatalf("stale row %+v", j)
	}
}
