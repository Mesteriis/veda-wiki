package knowledge_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/jobs"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// fakeExtractor finds known names as entities and emits a relation for each
// rule whose two names occur in the same block. It is deterministic.
type fakeExtractor struct {
	mu    sync.Mutex
	names map[string]string // surface name -> entity type
	rules [][3]string       // head name, relation, tail name
	calls int
	down  bool
	// gates block Extract for blocks containing the key until closed.
	gates   map[string]chan struct{}
	entered chan string
	// settings is the pipeline object reported by Ready and responses.
	settings json.RawMessage
	// failWith, when set, is returned by Extract.
	failWith error
	// busy calls answer ErrBusy before Extract works.
	busy int
}

func newFake() *fakeExtractor {
	return &fakeExtractor{
		names:    map[string]string{"billing": "service", "db1": "host", "Redis": "technology", "PostgreSQL": "technology", "Go": "technology", "ledger": "service", "secret-svc": "service"},
		rules:    [][3]string{{"billing", "runs_on", "db1"}, {"billing", "uses", "Redis"}, {"ledger", "uses", "PostgreSQL"}, {"secret-svc", "uses", "Redis"}},
		gates:    map[string]chan struct{}{},
		entered:  make(chan string, 16),
		settings: extractorSettings,
	}
}

func (f *fakeExtractor) setDown(down bool) { f.mu.Lock(); f.down = down; f.mu.Unlock() }

func (f *fakeExtractor) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func (f *fakeExtractor) gate(marker string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan struct{})
	f.gates[marker] = ch
	return ch
}

func (f *fakeExtractor) reconfigure(settings json.RawMessage) {
	f.mu.Lock()
	f.settings = settings
	f.mu.Unlock()
}

func (f *fakeExtractor) Ready(context.Context) (knowledge.ModelInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return knowledge.ModelInfo{}, knowledge.ErrUnavailable
	}
	return knowledge.ModelInfo{ID: knowledge.ModelID, Revision: knowledge.ModelRevision, Device: "cpu", Pipeline: f.settings}, nil
}

func (f *fakeExtractor) Extract(ctx context.Context, req knowledge.ExtractRequest) (knowledge.ExtractResult, error) {
	f.mu.Lock()
	f.calls++
	down, failWith := f.down, f.failWith
	if f.busy > 0 {
		f.busy--
		failWith = fmt.Errorf("%w: status 503 busy", knowledge.ErrBusy)
	}
	var wait chan struct{}
	for marker, ch := range f.gates {
		for _, b := range req.Blocks {
			if strings.Contains(b.Text, marker) {
				wait = ch
			}
		}
	}
	f.mu.Unlock()
	if down {
		return knowledge.ExtractResult{}, fmt.Errorf("%w: test outage", knowledge.ErrUnavailable)
	}
	if failWith != nil {
		return knowledge.ExtractResult{}, failWith
	}
	if wait != nil {
		f.entered <- req.RequestID
		select {
		case <-wait:
		case <-ctx.Done():
			return knowledge.ExtractResult{}, ctx.Err()
		}
	}
	f.mu.Lock()
	settings := f.settings
	f.mu.Unlock()
	res := knowledge.ExtractResult{SchemaVersion: 1, RequestID: req.RequestID, Model: knowledge.ModelInfo{ID: knowledge.ModelID, Revision: knowledge.ModelRevision, Device: "cpu", Pipeline: settings}}
	for _, b := range req.Blocks {
		br := knowledge.BlockResult{ID: b.ID, Chunks: []knowledge.ChunkResult{{Start: 0, End: utf8.RuneCountInString(b.Text), Feasible: true, Status: "ok"}}, Entities: []knowledge.EntitySpan{}, Relations: []knowledge.RelationSpan{}, Dropped: &knowledge.DroppedCounts{}}
		local := map[string]string{}
		for name, typ := range f.names {
			i := strings.Index(b.Text, name)
			if i < 0 {
				continue
			}
			start := utf8.RuneCountInString(b.Text[:i])
			id := fmt.Sprintf("e%d", len(br.Entities)+1)
			local[name] = id
			br.Entities = append(br.Entities, knowledge.EntitySpan{LocalID: id, Type: typ, Start: start, End: start + utf8.RuneCountInString(name), Text: name, Confidence: 0.9})
		}
		for _, r := range f.rules {
			if local[r[0]] != "" && local[r[2]] != "" {
				br.Relations = append(br.Relations, knowledge.RelationSpan{Type: r[1], Head: local[r[0]], Tail: local[r[2]], HeadConfidence: 0.8, TailConfidence: 0.7})
			}
		}
		res.Blocks = append(res.Blocks, br)
	}
	return res, nil
}

type harness struct {
	t      *testing.T
	st     *store.Store
	db     *jobs.DB
	ix     *memindex.Index
	ex     *fakeExtractor
	svc    *knowledge.Service
	cancel context.CancelFunc
	done   chan struct{}
	logs   []string
	logMu  sync.Mutex
}

func newHarness(t *testing.T, workers int) *harness {
	t.Helper()
	st := storetest.New(t)
	storetest.Source(t, st, "pub", "public")
	storetest.Source(t, st, "priv", "private")
	db, err := jobs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := &harness{t: t, st: st, db: db, ix: memindex.New(), ex: newFake()}
	h.svc = h.service(workers, knowledge.SchemaV1(), nil, "")
	return h
}

func (h *harness) config(workers int, s knowledge.Schema, aliases knowledge.Aliases, aliasDigest string) knowledge.Config {
	return knowledge.Config{Store: h.st, Manifest: h.db, Index: h.ix, Extractor: h.ex, Schema: s, Aliases: aliases, AliasDigest: aliasDigest, Workers: workers,
		ReconcileInterval: time.Hour, SignalDelay: time.Millisecond, ProbeBase: 5 * time.Millisecond, ProbeMax: 20 * time.Millisecond,
		Logf: func(format string, args ...any) {
			h.logMu.Lock()
			h.logs = append(h.logs, fmt.Sprintf(format, args...))
			h.logMu.Unlock()
		}}
}

func (h *harness) service(workers int, s knowledge.Schema, aliases knowledge.Aliases, aliasDigest string) *knowledge.Service {
	svc, err := knowledge.NewService(h.config(workers, s, aliases, aliasDigest))
	if err != nil {
		h.t.Fatal(err)
	}
	return svc
}

func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan struct{})
	h.st.OnCommit(h.svc.Signal)
	go func() { defer close(h.done); _ = h.svc.Run(ctx) }()
	h.t.Cleanup(h.stop)
}

func (h *harness) stop() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		h.t.Fatal("service did not stop")
	}
	h.cancel = nil
}

// settle reconciles until no job under the active fingerprint is pending
// or running.
func (h *harness) settle() knowledge.Status {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.svc.Reconcile(context.Background())
		st := h.svc.Status(context.Background())
		if st.Fingerprint != "" && st.LastReconcile != nil && st.Jobs[knowledge.JobPending]+st.Jobs[knowledge.JobRunning] == 0 && st.State != knowledge.StateDegraded {
			return st
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("jobs did not settle: %+v", h.svc.Status(context.Background()))
	return knowledge.Status{}
}

func digest(text string) string { return domain.Digest([]byte(text)) }

// write commits a new revision of an article.
func (h *harness) write(source, p, text string) knowledge.SnapshotKey {
	h.t.Helper()
	storetest.Mutate(h.t, h.st, func(db *domain.Database) {
		key := source + "/" + p
		a := db.Articles[key]
		a.SourceID, a.Path = source, p
		n := len(a.Versions) + 1
		if len(a.Versions) > 0 {
			n = a.Versions[len(a.Versions)-1].Number + 1
		}
		a.Versions = append(append([]domain.Revision(nil), a.Versions...), domain.Revision{Number: n, Text: text, SHA: digest(text), Created: "2026-11-04T00:00:00Z"})
		db.Articles[key] = a
	})
	return knowledge.SnapshotKey{SourceID: source, Path: p, SHA: digest(text)}
}

func (h *harness) remove(source, p string) {
	storetest.Mutate(h.t, h.st, func(db *domain.Database) { delete(db.Articles, source+"/"+p) })
}

// allowed is the owner's or the public allowed set of the current store.
func (h *harness) allowed(public bool) knowledge.AllowedSet {
	var set knowledge.AllowedSet
	_ = h.st.View(func(db *domain.Database) error {
		set = knowledge.Allowed(*db, public, knowledge.Scope{Owner: !public}, time.Now())
		return nil
	})
	return set
}

func (h *harness) fp() knowledge.Fingerprint {
	p, ok := h.svc.Pipeline()
	if !ok {
		h.t.Fatal("no active pipeline")
	}
	return p.Fingerprint
}

func (h *harness) article(key knowledge.SnapshotKey) (knowledge.ArticleView, error) {
	return h.ix.Article(context.Background(), h.allowed(false), h.fp(), key, 0)
}

func (h *harness) published() []knowledge.Snapshot {
	snaps, err := h.ix.Snapshots(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return snaps
}

func (h *harness) jobsOf(key knowledge.SnapshotKey) []knowledge.Job {
	jobs, err := h.db.Jobs(context.Background(), []knowledge.SnapshotKey{key})
	if err != nil {
		h.t.Fatal(err)
	}
	return jobs
}

func relations(v knowledge.ArticleView) []string {
	var out []string
	for _, c := range v.Claims {
		out = append(out, c.Relation)
	}
	slices.Sort(out) // claim order follows claim IDs, which hash the fingerprint
	return out
}
