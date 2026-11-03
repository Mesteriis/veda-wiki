package syncer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func TestSkippedProgramRechecksVisibilityAndImportsNewDocs(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"README.md": "# Project", "src/main.go": "package main"})
	var treeCalls, repoCalls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/trees/") {
			treeCalls.Add(1)
		}
		if r.URL.Path == "/repos/org/handbook" {
			repoCalls.Add(1)
		}
		f.Serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, github.NewForTest(gateway.URL, gateway.Client()))
	if _, err := m.Connect(context.Background(), ConnectRequest{ID: "program", Repository: "org/handbook"}); err != nil {
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
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	m = newSyncManager(reopened, github.NewForTest(gateway.URL, gateway.Client()))
	m.Once(context.Background())
	if treeCalls.Load() != firstTree || repoCalls.Load() != firstRepo+1 {
		t.Fatal("unchanged skip did not reuse tree or recheck visibility")
	}
	f.Advance(map[string]string{"docs/guide.md": "# New documentation"})
	m.Once(context.Background())
	if treeCalls.Load() != firstTree+1 {
		t.Fatal("changed head did not reload tree")
	}
	if _, err = reopened.page("program/docs/guide.md", true); err != nil {
		t.Fatal("new documentation not imported", err)
	}
}

func TestProgramDocsReplacedByFilePreservesLocalSnapshot(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Original guide"})
	m := newSyncManager(s, f.Client())
	if _, err := m.Connect(context.Background(), ConnectRequest{ID: "program", Repository: "org/handbook"}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	before := s.db().Sources["program"].ImportGeneration
	if _, err := s.page("program/docs/guide.md", true); err != nil {
		t.Fatal("initial public document unavailable", err)
	}
	f.Advance(map[string]string{"docs": "not a directory"})
	m.Once(context.Background())
	if c := s.connections()[0]; c.State != "error" || c.Verification != "unknown" || c.LastError != "invalid_docs_directory" {
		t.Fatal("invalid docs entry was accepted or misclassified", c.State, c.Verification, c.LastError)
	}
	if s.db().Sources["program"].ImportGeneration != before || s.db().Articles["program/docs/guide.md"].Deleted {
		t.Fatal("invalid docs entry changed the local snapshot")
	}
	if _, err := s.page("program/docs/guide.md", false); err != nil {
		t.Fatal("local documentation was lost", err)
	}
	if _, err := s.page("program/docs/guide.md", true); err == nil {
		t.Fatal("stale snapshot remained publicly visible")
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.page("program/docs/guide.md", false); err != nil {
		t.Fatal("document lost after restart", err)
	}
	f.Advance(map[string]string{"docs/guide.md": "# Original guide"})
	if err := reopened.retryConnection("program"); err != nil {
		t.Fatal(err)
	}
	m = newSyncManager(reopened, f.Client())
	m.Once(context.Background())
	if _, err := reopened.page("program/docs/guide.md", true); err != nil {
		t.Fatal("valid documentation did not recover", err)
	}
}

func TestProgramDocsSymlinkAndSubmoduleAreRejected(t *testing.T) {
	s := testStore(t)
	m := newSyncManager(s, nil)
	for _, entry := range []github.TreeEntry{
		{Path: "docs", Type: "blob", Mode: "120000"},
		{Path: "docs", Type: "commit", Mode: "160000"},
		{Path: "docs", Type: "tree", Mode: "160000"},
	} {
		_, err := m.remoteBatch(context.Background(), domain.GitHubConnection{ID: "program"}, strings.Repeat("a", 40), "", github.Tree{Tree: []github.TreeEntry{entry}})
		if github.ErrorCode(err) != "invalid_docs_directory" {
			t.Fatalf("invalid docs entry accepted: %s %s: %v", entry.Type, entry.Mode, err)
		}
	}
}

func TestPublicSnapshotHiddenWhileChangedHeadIsBeingChecked(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	var block atomic.Bool
	arrived, release := make(chan struct{}), make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block after the changed head is known, while its tree is fetched.
		if block.Load() && strings.Contains(r.URL.Path, "/git/trees/") {
			close(arrived)
			<-release
		}
		f.Serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, github.NewForTest(gateway.URL, gateway.Client()))
	if _, err := m.Connect(context.Background(), ConnectRequest{ID: "source", Repository: "org/handbook"}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if _, err := s.page("source/README.md", true); err != nil {
		t.Fatal("initial public article unavailable", err)
	}
	f.Advance(map[string]string{"README.md": "# No manifest"})
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := newSyncManager(s, f.Client())
	if _, err := m.Connect(context.Background(), ConnectRequest{ID: "source", Repository: "org/handbook"}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if _, err := s.page("source/README.md", true); err != nil {
		t.Fatal("initial public article unavailable", err)
	}
	c := s.connections()[0]
	write := false
	if _, err := m.Configure(context.Background(), ConfigureRequest{ID: c.ID, Write: &write, Expected: &c.ConfigRevision}); err != nil {
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
			f := githubtest.New(t, tc.original)
			m := newSyncManager(s, f.Client())
			if _, err := m.Connect(context.Background(), ConnectRequest{ID: "source", Repository: "org/handbook"}); err != nil {
				t.Fatal(err)
			}
			m.Once(context.Background())
			if _, err := s.page("source/"+tc.oldPath, true); err != nil {
				t.Fatal("initial public article unavailable", err)
			}
			f.Advance(tc.changed)
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
			reopened, err := s.reopen()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reopened.page("source/"+tc.oldPath, true); err == nil {
				t.Fatal("old snapshot became public after restart")
			}
			f.Advance(tc.original)
			if err := reopened.retryConnection("source"); err != nil {
				t.Fatal(err)
			}
			newSyncManager(reopened, f.Client()).Once(context.Background())
			if _, err := reopened.page("source/"+tc.oldPath, true); err != nil {
				t.Fatal("restored repository did not recover", err)
			}
		})
	}
}

func TestInvalidWikiMetadataPreservesSnapshotAndRecovers(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		files      map[string]string
	}{
		{"invalid_schema", "invalid_veda_metadata", map[string]string{"veda.yaml": metadataFixture + "unknown: value\n", "README.md": "# Changed"}},
		{"empty_manifest", "invalid_veda_metadata", map[string]string{"veda.yaml": "", "README.md": "# Changed"}},
		{"missing_image", "veda_image_unavailable", map[string]string{"veda.yaml": metadataFixture + "image: assets/cover.png\n", "README.md": "# Changed"}},
		{"invalid_image", "veda_image_unavailable", map[string]string{"veda.yaml": metadataFixture + "image: assets/cover.png\n", "assets/cover.png": "not an image", "README.md": "# Changed"}},
	} {
		for _, sqlite := range []bool{false, true} {
			name := tc.name + "/json"
			if sqlite {
				name = tc.name + "/sqlite"
			}
			t.Run(name, func(t *testing.T) {
				s := testStore(t)
				original := map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original wiki"}
				f := githubtest.New(t, original)
				m := newSyncManager(s, f.Client())
				if _, err := m.Connect(context.Background(), ConnectRequest{ID: "source", Repository: "org/handbook"}); err != nil {
					t.Fatal(err)
				}
				m.Once(context.Background())
				if sqlite {
					if err := store.MigrateSQLite(s.Dir()); err != nil {
						t.Fatal(err)
					}
					var err error
					s, err = s.reopen()
					if err != nil {
						t.Fatal(err)
					}
					m = newSyncManager(s, f.Client())
				}
				before := s.db().Sources["source"].ImportGeneration
				f.Advance(tc.files)
				m.Once(context.Background())
				c := s.connections()[0]
				if c.State != "error" || c.Verification != "unknown" || c.LastError != tc.code {
					t.Fatal("incorrect metadata failure state", c.State, c.LastError)
				}
				p, err := s.page("source/README.md", false)
				if err != nil || p.Text != "# Original wiki" || s.db().Sources["source"].ImportGeneration != before {
					t.Fatal("invalid metadata changed local snapshot", err)
				}
				if _, err := s.page("source/README.md", true); err == nil {
					t.Fatal("unverified public snapshot exposed")
				}
				s, err = s.reopen()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.page("source/README.md", false); err != nil {
					t.Fatal("local snapshot lost after restart", err)
				}
				f.Advance(original)
				if err := s.retryConnection("source"); err != nil {
					t.Fatal(err)
				}
				newSyncManager(s, f.Client()).Once(context.Background())
				if _, err := s.page("source/README.md", true); err != nil {
					t.Fatal("corrected metadata did not recover", err)
				}
			})
		}
	}
}

func TestWikiMetadataMustBeRegularFile(t *testing.T) {
	m := newSyncManager(testStore(t), nil)
	for _, entry := range []github.TreeEntry{
		{Path: "veda.yaml", Type: "blob", Mode: "120000"},
		{Path: "veda.yaml", Type: "tree", Mode: "040000"},
		{Path: "veda.yaml", Type: "commit", Mode: "160000"},
		{Path: "veda.yaml", Type: "blob", Mode: "100644", Size: 16385},
	} {
		_, err := m.remoteBatch(context.Background(), domain.GitHubConnection{ID: "source"}, strings.Repeat("a", 40), "", github.Tree{Tree: []github.TreeEntry{entry}})
		if github.ErrorCode(err) != "invalid_veda_metadata" {
			t.Fatal("invalid manifest accepted", entry.Mode, err)
		}
	}
}
