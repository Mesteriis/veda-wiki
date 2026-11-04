package app

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/jobs"
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
		"hostname http":     with("-knowledge-extractor-url", "http://localhost:8090"),
		"relative key":      with("-knowledge-extractor-key-file", "secret"),
		"symlink key":       with("-knowledge-extractor-key-file", link),
		"shared key":        with("-knowledge-extractor-key-file", secretFile(t, "k", 0644)),
		"empty key":         with("-knowledge-extractor-key-file", secretFile(t, "\n", 0600)),
		"workers":           with("-knowledge-workers", "5"),
		"interval":          with("-knowledge-reconcile-interval", "10s"),
		"neo4j with memory": with("-knowledge-neo4j-uri", "bolt://127.0.0.1:7687"),
		"neo4j missing":     with("-knowledge-index", "neo4j"),
		"neo4j remote bolt": with("-knowledge-index", "neo4j", "-knowledge-neo4j-uri", "bolt://10.0.0.1:7687", "-knowledge-neo4j-user", "neo4j", "-knowledge-neo4j-password-file", key),
		"neo4j not built":   with("-knowledge-index", "neo4j", "-knowledge-neo4j-uri", "bolt://127.0.0.1:7687", "-knowledge-neo4j-user", "neo4j", "-knowledge-neo4j-password-file", key),
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

// An enabled module (memory index) starts, asks the extractor for its
// settings, creates a private knowledge.sqlite and stops within the
// bounded shutdown.
func TestKnowledgeOnStartsAndStops(t *testing.T) {
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
	key := secretFile(t, "service-key\n", 0600)
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
