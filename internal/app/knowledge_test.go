package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/jobs"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Brief §12 case 1: without --knowledge nothing knowledge-related is
// opened or started and no knowledge file is created.
func TestKnowledgeOffOpensNothing(t *testing.T) {
	previous := openKnowledge
	t.Cleanup(func() { openKnowledge = previous })
	openKnowledge = func(string, *store.Store, *knowledgeFlags) (*knowledgeModule, error) {
		t.Error("knowledge opened while disabled")
		return nil, errors.New("disabled")
	}
	dataDir, err := runWithServe(t, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dataDir, jobs.File)); !os.IsNotExist(err) {
		t.Fatal("knowledge database created while disabled")
	}
	if _, err = runWithServe(t, func() error { return nil }, "-knowledge-workers", "2"); err == nil {
		t.Fatal("knowledge flag accepted without --knowledge")
	}
}

func secretFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKnowledgeFlagValidation(t *testing.T) {
	key := secretFile(t, "service-key\n", 0600)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(key, link); err != nil {
		t.Fatal(err)
	}
	base := []string{"-knowledge", "-knowledge-index", "memory", "-knowledge-extractor-url", "http://127.0.0.1:9", "-knowledge-extractor-key-file", key}
	with := func(pairs ...string) []string {
		out := append([]string(nil), base...)
		for i := 0; i < len(pairs); i += 2 {
			for j := range out {
				if out[j] == pairs[i] {
					out[j+1] = pairs[i+1]
					break
				}
				if j == len(out)-1 {
					out = append(out, pairs[i], pairs[i+1])
				}
			}
		}
		return out
	}
	cases := map[string][]string{
		"remote http":       with("-knowledge-extractor-url", "http://10.1.2.3:8090"),
		"hostname http":     with("-knowledge-extractor-url", "http://extractor:8090"),
		"relative key":      with("-knowledge-extractor-key-file", "secret"),
		"symlink key":       with("-knowledge-extractor-key-file", link),
		"shared key":        with("-knowledge-extractor-key-file", secretFile(t, "k", 0644)),
		"empty key":         with("-knowledge-extractor-key-file", secretFile(t, "\n", 0600)),
		"workers":           with("-knowledge-workers", "5"),
		"interval":          with("-knowledge-reconcile-interval", "10s"),
		"neo4j with memory": with("-knowledge-neo4j-uri", "bolt://127.0.0.1:7687"),
		"neo4j missing":     with("-knowledge-index", "neo4j"),
		"neo4j remote bolt": with("-knowledge-index", "neo4j", "-knowledge-neo4j-uri", "bolt://10.0.0.1:7687", "-knowledge-neo4j-user", "neo4j", "-knowledge-neo4j-password-file", key),
		"neo4j shared pass": with("-knowledge-index", "neo4j", "-knowledge-neo4j-uri", "bolt://127.0.0.1:7687", "-knowledge-neo4j-user", "neo4j", "-knowledge-neo4j-password-file", secretFile(t, "p", 0644)),
		"unknown index":     with("-knowledge-index", "sqlite"),
	}
	for name, args := range cases {
		dataDir, err := runWithServe(t, func() error { return nil }, args...)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "service-key") {
			t.Errorf("%s: error reveals the key", name)
		}
		if _, statErr := os.Stat(filepath.Join(dataDir, jobs.File)); !os.IsNotExist(statErr) {
			t.Errorf("%s: knowledge database created", name)
		}
	}
}

// An enabled module (memory index, or a Neo4j index whose server is
// unreachable) starts, asks the extractor for its settings, creates a
// private knowledge.sqlite and stops within the bounded shutdown: an
// unreachable Neo4j only degrades knowledge, it never blocks VEDA.
func TestKnowledgeOnStartsAndStops(t *testing.T) {
	key := secretFile(t, "service-key\n", 0600)
	password := secretFile(t, "neo4j-password\n", 0600)
	for name, index := range map[string][]string{
		"memory":            {"-knowledge-index", "memory"},
		"neo4j unreachable": {"-knowledge-index", "neo4j", "-knowledge-neo4j-uri", "bolt://127.0.0.1:1", "-knowledge-neo4j-user", "neo4j", "-knowledge-neo4j-password-file", password},
	} {
		t.Run(name, func(t *testing.T) { knowledgeStartsAndStops(t, key, index) })
	}
}

func knowledgeStartsAndStops(t *testing.T, key string, index []string) {
	ready := make(chan struct{}, 1)
	extractor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"status":"ready","model":{"id":"`+knowledge.ModelID+`","revision":"`+knowledge.ModelRevision+`","device":"cpu","pipeline":{"chunker_version":1}}}`)
		select {
		case ready <- struct{}{}:
		default:
		}
	}))
	defer extractor.Close()
	start := time.Now()
	dataDir, err := runWithServe(t, func() error {
		select {
		case <-ready:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("extractor never asked")
		}
	}, "-knowledge", "-knowledge-index", "memory", "-knowledge-extractor-url", extractor.URL, "-knowledge-extractor-key-file", key)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > shutdownTimeout {
		t.Fatal("shutdown was not bounded")
	}
	info, err := os.Lstat(filepath.Join(dataDir, jobs.File))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("knowledge database missing or not private", err)
	}
}

// slowExtractor ignores cancellation in Ready until released, like a call
// that outlives the drain timeout.
type slowExtractor struct {
	entered chan struct{}
	release chan struct{}
}

func (e slowExtractor) Ready(context.Context) (knowledge.ModelInfo, error) {
	e.entered <- struct{}{}
	<-e.release
	return knowledge.ModelInfo{}, knowledge.ErrUnavailable
}

func (e slowExtractor) Extract(context.Context, knowledge.ExtractRequest) (knowledge.ExtractResult, error) {
	return knowledge.ExtractResult{}, knowledge.ErrUnavailable
}

// close waits for Run to return before closing the manifest, even when Run
// is still busy after the parent context ended.
func TestKnowledgeCloseWaitsForRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := jobs.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ex := slowExtractor{entered: make(chan struct{}, 4), release: make(chan struct{})}
	svc, err := knowledge.NewService(knowledge.Config{Store: st, Manifest: db, Index: memindex.New(), Extractor: ex,
		Schema: knowledge.SchemaV1(), ReconcileInterval: time.Hour, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	indexClosed := make(chan struct{})
	m := &knowledgeModule{service: svc, db: db, closeIndex: func() { close(indexClosed) }}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	m.start(ctx, &workers)
	<-ex.entered
	cancel() // the drain gave up; Run is still inside Ready
	closed := make(chan struct{})
	go func() { m.close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("close returned while Run was still running")
	case <-indexClosed:
		t.Fatal("index closed while Run was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(ex.release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not return after Run stopped")
	}
	workers.Wait()
	if _, err := db.Counts(context.Background(), "kg1-x"); err == nil {
		t.Fatal("manifest still open after close")
	}
}
