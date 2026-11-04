package syncer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func TestReconcileSourcesPreservesIndependentChanges(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	for p, text := range map[string]string{"same.md": "# Same", "different.md": "# Local", "new.md": "# New"} {
		if _, err := s.put("local", p, text, 0); err != nil {
			t.Fatal(err)
		}
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "same.md": "# Same", "different.md": "# Remote", "remote-only.md": "# Remote only"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	in := ReconcileRequest{SourceID: "local", DestinationID: "handbook", Limit: 50}
	r, err := s.reconcileSources(context.Background(), in)
	if err != nil || r.Baselines != 1 || r.Created != 1 || r.Updated != 0 || len(r.Blocked) != 1 || r.Blocked[0] != "different.md" {
		t.Fatalf("unexpected initial result %+v %v", r, err)
	}
	if s.db().Version != domain.RevisionsFormat || s.db().Articles["handbook/remote-only.md"].Deleted || f.WriteCount() != 0 {
		t.Fatal("unsafe initial reconciliation")
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.Contents()["new.md"] != "# New" {
		t.Fatal("new article not synchronized")
	}
	if _, err = s.put("local", "same.md", "# Local updated", 1); err != nil {
		t.Fatal(err)
	}
	r, err = s.reconcileSources(context.Background(), in)
	if err != nil || r.Updated != 1 {
		t.Fatal("safe source update not queued", r, err)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.Contents()["same.md"] != "# Local updated" {
		t.Fatal("source update not confirmed")
	}
	contents := f.Contents()
	contents["same.md"] = "# Independent remote"
	f.Advance(contents)
	m.Once(context.Background())
	if _, err = s.put("local", "same.md", "# Independent local", 2); err != nil {
		t.Fatal(err)
	}
	r, err = s.reconcileSources(context.Background(), in)
	if err != nil || !containsPath(r.Blocked, "same.md") || s.db().Articles["handbook/same.md"].Versions[len(s.db().Articles["handbook/same.md"].Versions)-1].Text != "# Independent remote" {
		t.Fatal("divergence overwritten", r, err)
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Version != domain.RevisionsFormat || reopened.db().Articles["handbook/same.md"].ReconcileBase == nil {
		t.Fatal("baseline lost after reopen", err)
	}
}

func TestReconcileBoundedAndResumable(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	for _, p := range []string{"a.md", "b.md", "c.md"} {
		if _, err := s.put("local", p, "# Same "+p, 0); err != nil {
			t.Fatal(err)
		}
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same a.md", "b.md": "# Same b.md", "c.md": "# Same c.md"})
	f.Modify(func() { f.Private = true })
	connectFixture(t, s, f, true)
	in := ReconcileRequest{SourceID: "local", DestinationID: "handbook", Limit: 1}
	for i := 0; i < 3; i++ {
		r, err := s.reconcileSources(context.Background(), in)
		if err != nil || r.Baselines != 1 || r.More != (i < 2) {
			t.Fatal("bounded pass failed", r, err)
		}
	}
	r, err := s.reconcileSources(context.Background(), in)
	if err != nil || r.Unchanged != 3 || r.More {
		t.Fatal("replay not idempotent", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.reconcileSources(ctx, in); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}

func TestReconcileSQLiteSurvivesReopen(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.Modify(func() { f.Private = true })
	connectFixture(t, s, f, true)
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.reconcileSources(context.Background(), ReconcileRequest{SourceID: "local", DestinationID: "handbook"})
	if err != nil || r.Baselines != 1 {
		t.Fatal("baseline failed", r, err)
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Version != domain.RevisionsFormat || reopened.db().Articles["handbook/a.md"].ReconcileBase == nil {
		t.Fatal("sqlite baseline lost", err)
	}
}

func TestReconcileSkipsSecretLikeAndMissingDependencies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "secret.md", "password=fixture-only-secret", 0)
	s.put("local", "broken.md", "[Missing](missing.md)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture})
	f.Modify(func() { f.Private = true })
	connectFixture(t, s, f, true)
	r, err := s.reconcileSources(context.Background(), ReconcileRequest{SourceID: "local", DestinationID: "handbook"})
	if err != nil || len(r.Blocked) != 2 || len(s.jobs()) != 0 || len(s.db().Articles) != 2 {
		t.Fatal("unsafe data queued", r, err)
	}
}

func TestOldBinaryRejectsReconciliationStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V18_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.Modify(func() { f.Private = true })
	connectFixture(t, s, f, true)
	if _, err := s.reconcileSources(context.Background(), ReconcileRequest{SourceID: "local", DestinationID: "handbook"}); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(s.Dir(), "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, old, "--data", s.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(out), "unsupported store") {
		t.Fatal("old binary did not reject the store")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(after) != string(before) {
		t.Fatal("old binary changed the store state")
	}
}

func TestReconcileEqualArticleQueuesMissingImageAndBlocksDifferentImage(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "different"}[different], func(t *testing.T) {
			s := testStore(t)
			source(t, s, "local", "private")
			ref := fixtureAsset(t, s, "local", "img/icon.png")
			text := "# Same\n![icon](img/icon.png)"
			s.put("local", "a.md", text, 0)
			files := map[string]string{"veda.yaml": metadataFixture, "a.md": text}
			if different {
				files["img/icon.png"] = string(append(smallPNG(t), 1))
			}
			f := githubtest.New(t, files)
			f.Modify(func() { f.Private = true })
			m := connectFixture(t, s, f, true)
			r, err := s.reconcileSources(context.Background(), ReconcileRequest{SourceID: "local", DestinationID: "handbook"})
			if err != nil {
				t.Fatal(err)
			}
			if different {
				if !containsPath(r.Blocked, "a.md") || len(s.jobs()) != 0 {
					t.Fatal("different image was overwritten", r)
				}
				return
			}
			if r.Updated != 1 || len(s.jobs()) != 1 || s.jobs()[0].Assets["img/icon.png"] != ref {
				t.Fatal("missing image not queued", r)
			}
			m.Once(context.Background())
			m.Once(context.Background())
			if domain.Digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA || s.jobs()[0].State != "complete" {
				t.Fatal("missing image not confirmed")
			}
		})
	}
}

func TestReconcileRejectsInvalidStoredBaseline(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	source(t, s, "target", "private")
	s.put("local", "a.md", "# A", 0)
	s.put("target", "a.md", "# A", 0)
	b := s.db().Articles["target/a.md"]
	b.ReconcileBase = &domain.ReconcileBase{SourceID: "local", Path: "a.md", Revision: 1, SHA: "invalid"}
	s.mutate(func(db *domain.Database) {
		db.Version = 18
		db.Articles["target/a.md"] = b
	})
	if _, err := s.reopen(); err == nil {
		t.Fatal("invalid baseline accepted on reopen")
	}
}

func TestOldBinaryRejectsReconciliationSQLite(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V18_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.Modify(func() { f.Private = true })
	connectFixture(t, s, f, true)
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.reconcileSources(context.Background(), ReconcileRequest{SourceID: "local", DestinationID: "handbook"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(s.Dir(), "state.sqlite")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, old, "--data", s.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(out), "unsupported store") {
		t.Fatal("old binary did not reject the SQLite store")
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(before) {
		t.Fatal("old binary changed the SQLite store state")
	}
}

func TestConcurrentReconcileDoesNotDuplicateWrites(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "same.md", "# Same", 0)
	s.put("local", "new.md", "# New", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "same.md": "# Same"})
	f.Modify(func() { f.Private = true })
	connectFixture(t, s, f, true)
	type outcome struct {
		result ReconcileResult
		err    error
	}
	out := make(chan outcome, 8)
	for i := 0; i < 8; i++ {
		go func() {
			r, err := s.reconcileSources(context.Background(), ReconcileRequest{SourceID: "local", DestinationID: "handbook"})
			out <- outcome{r, err}
		}()
	}
	baselines, created := 0, 0
	for i := 0; i < 8; i++ {
		r := <-out
		if r.err != nil {
			t.Fatal(r.err)
		}
		baselines += r.result.Baselines
		created += r.result.Created
	}
	if baselines != 1 || created != 1 || len(s.jobs()) != 1 || len(s.db().Articles["handbook/new.md"].Versions) != 1 {
		t.Fatal("concurrent reconciliations duplicated writes")
	}
}
