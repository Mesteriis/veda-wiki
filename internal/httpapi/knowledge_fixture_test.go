package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/jobs"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
)

// kgSettings is the synthetic pipeline object the fake extractor reports.
var kgSettings = json.RawMessage(`{"decoder":{"library":"synthetic-test"},"window_tokens":512}`)

// kgExtractor is a deterministic fake extractor: known names become
// entities, and a rule whose two names occur in one block becomes a claim.
// A block containing "REJECT" is refused permanently.
type kgExtractor struct {
	mu   sync.Mutex
	down bool
}

var kgNames = map[string]string{"billing": "service", "ledger": "service", "secret-svc": "service", "db1": "host", "DB1": "host", "Redis": "technology", "PostgreSQL": "technology"}

var kgRules = [][3]string{{"billing", "runs_on", "db1"}, {"billing", "uses", "Redis"}, {"ledger", "uses", "PostgreSQL"}, {"secret-svc", "uses", "Redis"}, {"secret-svc", "runs_on", "DB1"}}

func (f *kgExtractor) setDown(down bool) { f.mu.Lock(); f.down = down; f.mu.Unlock() }

func (f *kgExtractor) Ready(context.Context) (knowledge.ModelInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return knowledge.ModelInfo{}, knowledge.ErrUnavailable
	}
	return knowledge.ModelInfo{ID: knowledge.ModelID, Revision: knowledge.ModelRevision, Device: "cpu", Pipeline: kgSettings}, nil
}

func (f *kgExtractor) Extract(_ context.Context, req knowledge.ExtractRequest) (knowledge.ExtractResult, error) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()
	if down {
		return knowledge.ExtractResult{}, knowledge.ErrUnavailable
	}
	res := knowledge.ExtractResult{SchemaVersion: 1, RequestID: req.RequestID, Model: knowledge.ModelInfo{ID: knowledge.ModelID, Revision: knowledge.ModelRevision, Device: "cpu", Pipeline: kgSettings}}
	for _, b := range req.Blocks {
		if strings.Contains(b.Text, "REJECT") {
			return knowledge.ExtractResult{}, fmt.Errorf("%w: synthetic", knowledge.ErrRejected)
		}
		br := knowledge.BlockResult{ID: b.ID, Chunks: []knowledge.ChunkResult{{Start: 0, End: utf8.RuneCountInString(b.Text), Feasible: true, Status: "ok"}}, Entities: []knowledge.EntitySpan{}, Relations: []knowledge.RelationSpan{}, Dropped: &knowledge.DroppedCounts{}}
		local := map[string]string{}
		for _, name := range []string{"billing", "ledger", "secret-svc", "db1", "DB1", "Redis", "PostgreSQL"} {
			rest, offset := b.Text, 0
			for {
				i := strings.Index(rest, name)
				if i < 0 {
					break
				}
				start := utf8.RuneCountInString(b.Text[:offset+i])
				id := fmt.Sprintf("e%d", len(br.Entities)+1)
				if local[name] == "" {
					local[name] = id
				}
				br.Entities = append(br.Entities, knowledge.EntitySpan{LocalID: id, Type: kgNames[name], Start: start, End: start + utf8.RuneCountInString(name), Text: name, Confidence: 0.9})
				offset += i + len(name)
				rest = b.Text[offset:]
			}
		}
		for _, r := range kgRules {
			if local[r[0]] != "" && local[r[2]] != "" {
				br.Relations = append(br.Relations, knowledge.RelationSpan{Type: r[1], Head: local[r[0]], Tail: local[r[2]], HeadConfidence: 0.8, TailConfidence: 0.7})
			}
		}
		res.Blocks = append(res.Blocks, br)
	}
	return res, nil
}

// gatedManifest blocks Reindex until release is closed, to hold one
// reindex operation open.
type gatedManifest struct {
	knowledge.Manifest
	gate    atomic.Pointer[chan struct{}]
	entered chan struct{}
}

func (m *gatedManifest) Reindex(ctx context.Context, fp knowledge.Fingerprint, sourceID, p string, force bool, now time.Time) (int, error) {
	if g := m.gate.Load(); g != nil {
		m.entered <- struct{}{}
		<-*g
	}
	return m.Manifest.Reindex(ctx, fp, sourceID, p, force, now)
}

// slowIndex makes reads wait for their deadline while slow is set.
type slowIndex struct {
	knowledge.Index
	slow atomic.Bool
}

func (ix *slowIndex) Entities(ctx context.Context, allowed knowledge.AllowedSet, fp knowledge.Fingerprint, q knowledge.EntityQuery) (knowledge.EntityPage, error) {
	if ix.slow.Load() {
		<-ctx.Done()
		return knowledge.EntityPage{}, ctx.Err()
	}
	return ix.Index.Entities(ctx, allowed, fp, q)
}

// kgHarness is a store with the knowledge module running over a memory
// index and the fake extractor.
type kgHarness struct {
	t        *testing.T
	s        *Store
	ex       *kgExtractor
	ix       *slowIndex
	mem      *memindex.Index
	manifest *gatedManifest
	svc      *knowledge.Service
	sessions *auth.Sessions
	scoped   string
}

func newKGHarness(t *testing.T) *kgHarness { t.Helper(); return newKGHarnessWith(t, 2*time.Second) }

// newKGHarnessWith sets the read timeout of the service.
func newKGHarnessWith(t *testing.T, readTimeout time.Duration) *kgHarness {
	t.Helper()
	s := testStore(t)
	db, err := jobs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := &kgHarness{t: t, s: s, ex: &kgExtractor{}, mem: memindex.New(), manifest: &gatedManifest{Manifest: db, entered: make(chan struct{}, 1)}, sessions: &auth.Sessions{}}
	h.ix = &slowIndex{Index: h.mem}
	h.svc, err = knowledge.NewService(knowledge.Config{Store: s.Store, Manifest: h.manifest, Index: h.ix, Extractor: h.ex, Schema: knowledge.SchemaV1(),
		ReconcileInterval: time.Hour, SignalDelay: time.Millisecond, ProbeBase: 5 * time.Millisecond, ProbeMax: 20 * time.Millisecond, ReadTimeout: readTimeout})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *kgHarness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h.s.OnCommit(h.svc.Signal)
	go func() { defer close(done); _ = h.svc.Run(ctx) }()
	h.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			h.t.Error("knowledge service did not stop")
		}
	})
}

// settle reconciles until no job under the active fingerprint waits.
func (h *kgHarness) settle() {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_ = h.svc.Reconcile(context.Background())
		st := h.svc.Status(context.Background())
		if st.Fingerprint != "" && st.LastReconcile != nil && st.Jobs[knowledge.JobPending]+st.Jobs[knowledge.JobRunning] == 0 && st.State != knowledge.StateDegraded {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("knowledge did not settle: %+v", h.svc.Status(context.Background()))
}

const (
	kgCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	kgTree   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// The GitHub article's published copy and its newer private draft.
	kgPublished = "# Page\n\nbilling uses Redis.\n"
	kgDraft     = "# Page\n\nledger uses PostgreSQL.\n"
)

// seed creates the sources of the scenarios: pub (public local), gh
// (public GitHub source whose published copy differs from the current
// revision), alpha and beta (private), and an access key for alpha only.
func (h *kgHarness) seed() {
	h.t.Helper()
	source(h.t, h.s, "pub", "public")
	source(h.t, h.s, "alpha", "private")
	source(h.t, h.s, "beta", "private")
	h.put("pub", "billing.md", "# Billing\n\nbilling runs on db1 and uses Redis.\n")
	h.put("pub", "guide.md", "# Guide\n\nbilling is described here; Redis caches.\n")
	h.put("alpha", "guide.md", "# Guide\n\nbilling and ledger uses PostgreSQL; Redis caches.\n")
	h.s.mutate(h.t, func(db *domain.Database) {
		db.Sources["gh"] = domain.Source{ID: "gh", Title: "gh", Kind: "wiki", Visibility: "public", ConnectionID: "c1", ImportCommit: kgCommit}
		if db.Connections == nil {
			db.Connections = map[string]domain.GitHubConnection{}
		}
		db.Connections["c1"] = domain.GitHubConnection{ID: "c1", Verification: "verified", VerifiedAt: time.Now().UTC().Format(time.RFC3339Nano), LastPull: kgCommit, TreeSHA: kgTree}
		published := domain.Revision{Number: 1, Text: kgPublished, SHA: domain.Digest([]byte(kgPublished)), Created: "2026-11-04T00:00:00Z"}
		draft := domain.Revision{Number: 2, Text: kgDraft, SHA: domain.Digest([]byte(kgDraft)), Created: "2026-11-04T00:00:00Z"}
		db.Articles["gh/page.md"] = domain.Article{SourceID: "gh", Path: "page.md", Versions: []domain.Revision{published, draft}, Published: &published}
	})
	var err error
	if h.scoped, err = h.s.auth().CreateAccessKey("agent", []string{"alpha"}, time.Now().Add(time.Hour)); err != nil {
		h.t.Fatal(err)
	}
}

func (h *kgHarness) put(sourceID, p, text string) {
	h.t.Helper()
	expected := 0
	if a, ok := h.s.state(h.t).Articles[sourceID+"/"+p]; ok {
		expected = len(a.Versions)
	}
	if _, err := h.s.put(sourceID, p, text, expected); err != nil {
		h.t.Fatal(err)
	}
}

// wiki is the private listener with the module on.
func (h *kgHarness) wiki() Wiki {
	return Wiki{Store: h.s, Token: "test-owner", ReaderToken: "test-reader", Sessions: h.sessions, AdminSessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}, Knowledge: h.svc}
}

func (h *kgHarness) public() Wiki { w := h.wiki(); w.Public = true; return w }

// get calls a knowledge route as the given bearer (empty: anonymous).
func kgGet(w Wiki, path, bearer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://localhost:8080"+path, nil)
	r.RemoteAddr = "127.0.0.1:1234"
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	out := httptest.NewRecorder()
	w.ServeHTTP(out, r)
	return out
}

func kgDecode[T any](t *testing.T, out *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if out.Code != 200 {
		t.Fatalf("status %d: %s", out.Code, out.Body.String())
	}
	if err := json.Unmarshal(out.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// entityID finds an entity by name in an entities answer.
func kgEntityID(t *testing.T, w Wiki, bearer, name, sourceID string) string {
	t.Helper()
	page := kgDecode[knowledge.EntityPage](t, kgGet(w, "/api/v1/knowledge/entities?q="+name, bearer))
	for _, e := range page.Entities {
		if e.Name == name && (sourceID == "" || strings.Contains(e.Key, "@"+sourceID+":") || !strings.Contains(e.Key, "@")) {
			return e.ID
		}
	}
	t.Fatalf("entity %s not found: %+v", name, page)
	return ""
}

func kgErrorCode(t *testing.T, out *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
		t.Fatalf("not a knowledge error: %d %q", out.Code, out.Body.String())
	}
	return body.Error.Code
}
