package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSkippedProgramRechecksVisibilityAndImportsNewDocs(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"README.md": "# Project", "src/main.go": "package main"})
	var treeCalls, repoCalls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/trees/") {
			treeCalls.Add(1)
		}
		if r.URL.Path == "/repos/org/handbook" {
			repoCalls.Add(1)
		}
		f.serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, &GitHubClient{base: gateway.URL, http: gateway.Client()})
	if _, err := m.connect(context.Background(), connectRequest{ID: "program", Repository: "org/handbook"}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if c := s.connections()[0]; c.State != "skipped" || c.LastPull == "" || c.TreeSHA == "" {
		t.Fatal("skip was not recorded", c.State)
	}
	if len(s.snapshot(false).Pages) != 0 {
		t.Fatal("imported a repository without docs")
	}
	firstTree, firstRepo := treeCalls.Load(), repoCalls.Load()
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	m = newSyncManager(reopened, &GitHubClient{base: gateway.URL, http: gateway.Client()})
	m.Once(context.Background())
	if treeCalls.Load() != firstTree || repoCalls.Load() != firstRepo+1 {
		t.Fatal("unchanged skip did not reuse tree or recheck visibility")
	}
	f.advance(map[string]string{"docs/guide.md": "# New documentation"})
	m.Once(context.Background())
	if treeCalls.Load() != firstTree+1 {
		t.Fatal("changed head did not reload tree")
	}
	if _, err = reopened.page("program/docs/guide.md", true); err != nil {
		t.Fatal("new documentation not imported", err)
	}
}

func TestPublicSnapshotHiddenWhileChangedHeadIsBeingChecked(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	var block atomic.Bool
	arrived, release := make(chan struct{}), make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block.Load() && strings.Contains(r.URL.Path, "/git/ref/heads/") {
			close(arrived)
			<-release
		}
		f.serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, &GitHubClient{base: gateway.URL, http: gateway.Client()})
	if _, err := m.connect(context.Background(), connectRequest{ID: "source", Repository: "org/handbook"}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if _, err := s.page("source/README.md", true); err != nil {
		t.Fatal("initial public article unavailable", err)
	}
	f.advance(map[string]string{"README.md": "# No manifest"})
	block.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); m.Once(context.Background()) }()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		close(release)
		<-done
		t.Fatal("head request did not start")
	}
	if err := s.retryConnection("source"); err != nil {
		close(release)
		<-done
		t.Fatal("manual retry during sync failed", err)
	}
	_, publicErr := s.page("source/README.md", true)
	_, privateErr := s.page("source/README.md", false)
	close(release)
	<-done
	if publicErr == nil || privateErr != nil {
		t.Fatal("snapshot visibility during sync", publicErr, privateErr)
	}
	if _, err := s.page("source/README.md", true); err == nil {
		t.Fatal("invalidated snapshot became public after sync")
	}
}

func TestReconfigurationRequiresFreshImportedHead(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := newSyncManager(s, f.client())
	if _, err := m.connect(context.Background(), connectRequest{ID: "source", Repository: "org/handbook"}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if _, err := s.page("source/README.md", true); err != nil {
		t.Fatal("initial public article unavailable", err)
	}
	c := s.connections()[0]
	write := false
	if _, err := m.configure(context.Background(), configureRequest{ID: c.ID, Write: &write, Expected: &c.ConfigRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.page("source/README.md", true); err == nil {
		t.Fatal("reconfigured connection exposed an unverified snapshot")
	}
	if _, err := s.page("source/README.md", false); err != nil {
		t.Fatal("reconfiguration removed local content", err)
	}
	m.Once(context.Background())
	if _, err := s.page("source/README.md", true); err != nil {
		t.Fatal("fresh pull did not restore public content", err)
	}
}

func TestChangedRepositoryTypeClosesPublicSnapshotAndRecovers(t *testing.T) {
	for _, tc := range []struct {
		name, oldPath     string
		original, changed map[string]string
	}{
		{"wiki_manifest_removed", "README.md", map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original wiki"}, map[string]string{"README.md": "# No manifest"}},
		{"program_becomes_wiki", "docs/guide.md", map[string]string{"docs/guide.md": "# Original guide"}, map[string]string{"veda.yaml": metadataFixture, "README.md": "# New wiki"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			f := newFakeGitHub(t, tc.original)
			m := newSyncManager(s, f.client())
			if _, err := m.connect(context.Background(), connectRequest{ID: "source", Repository: "org/handbook"}); err != nil {
				t.Fatal(err)
			}
			m.Once(context.Background())
			if _, err := s.page("source/"+tc.oldPath, true); err != nil {
				t.Fatal("initial public article unavailable", err)
			}
			f.advance(tc.changed)
			m.Once(context.Background())
			if c := s.connections()[0]; c.State != "error" || c.LastError != "repository_type_changed" || c.Verification != "unknown" {
				t.Fatal("type change not reported safely", c.State, c.LastError, c.Verification)
			}
			if _, err := s.page("source/"+tc.oldPath, true); err == nil {
				t.Fatal("old snapshot remains public")
			}
			if _, err := s.page("source/"+tc.oldPath, false); err != nil {
				t.Fatal("local article was lost", err)
			}
			reopened, err := openStore(s.dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reopened.page("source/"+tc.oldPath, true); err == nil {
				t.Fatal("old snapshot became public after restart")
			}
			f.advance(tc.original)
			if err := reopened.retryConnection("source"); err != nil {
				t.Fatal(err)
			}
			newSyncManager(reopened, f.client()).Once(context.Background())
			if _, err := reopened.page("source/"+tc.oldPath, true); err != nil {
				t.Fatal("restored repository did not recover", err)
			}
		})
	}
}
