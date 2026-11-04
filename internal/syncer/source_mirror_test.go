package syncer

import (
	"context"
	"errors"
	"fmt"
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

func TestMirrorBackgroundUpdatesAndPause(t *testing.T) {
	s, m, f := mirrorFixture(t)
	enabled := true
	expected := 0
	conf, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &enabled, ExpectedRevision: &expected})
	if err != nil || conf.Revision != 1 || s.db().Version != 19 {
		t.Fatal("enable failed", err)
	}
	m.Once(context.Background())
	if s.db().Articles["handbook/a.md"].ReconcileBase == nil || s.mirrors()[0].State != "ready" {
		t.Fatal("baseline not created")
	}
	if _, err = s.put("local", "a.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if len(s.jobs()) != 1 || s.jobs()[0].State != "pending" {
		t.Fatal("update not queued")
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.Contents()["a.md"] != "# Changed" || s.jobs()[0].State != "complete" {
		t.Fatal("update not confirmed")
	}
	paused := false
	expected = 1
	if _, err = s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &paused, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("local", "a.md", "# After pause", 2); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if len(s.jobs()) != 1 || f.Contents()["a.md"] != "# Changed" || s.mirrors()[0].State != "paused" {
		t.Fatal("paused mirror continued")
	}
	r, err := s.reopen()
	if err != nil || r.db().Version != 19 || len(r.mirrors()) != 1 || r.mirrors()[0].Enabled {
		t.Fatal("configuration did not survive reopen", err)
	}
}

func TestMirrorKeepsConflictsAndDoesNotPublish(t *testing.T) {
	s, m, f := mirrorFixture(t)
	enabled := true
	expected := 0
	if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &enabled, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	remote := f.Contents()
	remote["a.md"] = "# Remote change"
	f.Advance(remote)
	m.Once(context.Background())
	if _, err := s.put("local", "a.md", "# Local change", 1); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if s.mirrors()[0].State != "blocked" || s.mirrors()[0].Blocked != 1 || f.Contents()["a.md"] != "# Remote change" || len(s.jobs()) != 0 {
		t.Fatal("divergence overwritten")
	}
	view := s.mirrors()
	if len(view[0].BlockedPaths) != 1 || view[0].BlockedPaths[0] != "a.md" {
		t.Fatal("blocked path missing")
	}
	view[0].BlockedPaths[0] = "changed"
	if s.mirrors()[0].BlockedPaths[0] != "a.md" {
		t.Fatal("mirror status exposed mutable state")
	}
	f.Modify(func() { f.Private = false })
	m.Once(context.Background())
	if s.mirrors()[0].State != "error" || f.WriteCount() != 0 {
		t.Fatal("public destination accepted")
	}
}

func TestMirrorSQLiteSurvivesReopen(t *testing.T) {
	s, _, f := mirrorFixture(t)
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	expected := 0
	if _, err = s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &enabled, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	s, err = s.reopen()
	if err != nil || len(s.mirrors()) != 1 || !s.mirrors()[0].Enabled {
		t.Fatal("mirror lost on reopen", err)
	}
	m := newSyncManager(s, f.Client())
	m.Once(context.Background())
	if _, err = s.put("local", "a.md", "# Edited after reopen", 1); err != nil {
		t.Fatal(err)
	}
	s, err = s.reopen()
	if err != nil || s.mirrors()[0].State != "ready" || s.db().Articles["handbook/a.md"].ReconcileBase == nil {
		t.Fatal("background status not persisted", err)
	}
}

func TestMirrorPauseRetainsQueuedWrite(t *testing.T) {
	s, m, f := mirrorFixture(t)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	s.put("local", "a.md", "# Queued", 1)
	m.Once(context.Background())
	if len(s.jobs()) != 1 {
		t.Fatal("write not queued")
	}
	no := false
	revision = 1
	if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &no, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	s.put("local", "a.md", "# After pause", 2)
	m.Once(context.Background())
	m.Once(context.Background())
	if f.Contents()["a.md"] != "# Queued" || len(s.jobs()) != 1 || s.jobs()[0].State != "complete" {
		t.Fatal("pause discarded queued write or created another")
	}
}

func TestMirrorMultipleBatchesEventuallyConverge(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	for i := 0; i < 22; i++ {
		name := fmt.Sprintf("p%02d.md", i)
		s.put("local", name, "# "+name, 0)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		m.Once(context.Background())
		if s.mirrors()[0].State == "ready" {
			break
		}
	}
	if s.mirrors()[0].State != "ready" || len(f.Contents()) != 23 {
		t.Fatal("multiple batches did not converge", s.mirrors()[0].State, len(f.Contents()))
	}
	for _, job := range s.jobs() {
		if job.State != "complete" {
			t.Fatal("unfinished job", job.State)
		}
	}
}

func TestOldBinaryRejectsMirrorStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V19_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	for _, sqlite := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sqlite"}[sqlite], func(t *testing.T) {
			s, _, _ := mirrorFixture(t)
			if sqlite {
				if err := store.MigrateSQLite(s.Dir()); err != nil {
					t.Fatal(err)
				}
				var err error
				s, err = s.reopen()
				if err != nil {
					t.Fatal(err)
				}
			}
			yes := true
			revision := 0
			if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
				t.Fatal(err)
			}
			name := "state.json"
			if sqlite {
				name = "state.sqlite"
			}
			file := filepath.Join(s.Dir(), name)
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, old, "--data", s.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || ctx.Err() != nil || (!strings.Contains(string(out), "unsupported store") && !strings.Contains(string(out), "unknown SQLite record category")) {
				t.Fatalf("old binary did not reject mirror store: %s", out)
			}
			after, err := os.ReadFile(file)
			if err != nil || string(after) != string(before) {
				t.Fatal("old binary changed mirror store")
			}
		})
	}
}

func TestMirrorSQLiteManyMatchingArticles(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	remote := map[string]string{"veda.yaml": metadataFixture}
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("p%03d.md", i)
		text := "# " + name
		s.put("local", name, text, 0)
		remote[name] = text
	}
	f := githubtest.New(t, remote)
	f.Modify(func() { f.Private = true })
	connectFixture(t, s, f, true)
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	revision := 0
	if _, err = s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	m := newSyncManager(s, f.Client())
	for i := 0; i < 6; i++ {
		m.Once(context.Background())
		if s.mirrors()[0].State == "ready" {
			break
		}
	}
	if s.mirrors()[0].State != "ready" {
		t.Fatal("matching batch did not converge", s.mirrors()[0].State)
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, a := range reopened.db().Articles {
		if a.SourceID == "handbook" && a.ReconcileBase != nil {
			count++
		}
	}
	if count != 100 || len(s.jobs()) != 0 {
		t.Fatal("baseline batch not durable", count)
	}
}

func TestMirrorWorkerRunsOnIntervalAndStops(t *testing.T) {
	s, m, f := mirrorFixture(t)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx, 20*time.Millisecond) }()
	defer func() { cancel(); <-done }()
	await := func(check func() bool) {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			if check() {
				return
			}
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatal("background worker did not converge")
			}
		}
	}
	await(func() bool { return s.mirrors()[0].State == "ready" })
	if _, err := s.put("local", "a.md", "# Periodic", 1); err != nil {
		t.Fatal(err)
	}
	await(func() bool { return f.Contents()["a.md"] == "# Periodic" && s.mirrors()[0].State == "ready" })
}

func TestMirrorCancelledWhileWaitingDoesNotChangeConfiguration(t *testing.T) {
	s, _, _ := mirrorFixture(t)
	yes := true
	revision := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := s.holdLock()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := s.configureMirror(ctx, MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision})
		done <- err
	}()
	<-started
	cancel()
	release()
	err := <-done
	if !errors.Is(err, context.Canceled) || len(s.mirrors()) != 0 {
		t.Fatal("cancelled configuration committed", err)
	}
}

func TestMirrorQueuedPrivateWriteNeverPublishesAfterVisibilityChange(t *testing.T) {
	s, m, f := mirrorFixture(t)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	s.put("local", "a.md", "# Private local update", 1)
	m.Once(context.Background())
	if len(s.jobs()) != 1 || s.jobs()[0].Visibility != "private" {
		t.Fatal("private job missing")
	}
	f.Modify(func() { f.Private = false })
	m.Once(context.Background())
	if f.WriteCount() != 0 || f.Contents()["a.md"] != "# Same" || s.mirrors()[0].State != "error" {
		t.Fatal("private mirror write reached public repository")
	}
}

func TestMirrorConnectedSourcesAndCycleGuard(t *testing.T) {
	s, m, first, second := connectedMoveFixtures(t)
	files := second.Contents()
	files["README.md"] = "# Source"
	second.Advance(files)
	m.Once(context.Background())
	enabled, revision := true, 0
	in := MirrorRequest{SourceID: "one", DestinationID: "two", Enabled: &enabled, ExpectedRevision: &revision}
	if _, err := s.configureMirror(context.Background(), in); err != nil {
		t.Fatal("connected source rejected", err)
	}
	m.Once(context.Background())
	files = first.Contents()
	files["README.md"] = "# Changed upstream"
	first.Advance(files)
	for i := 0; i < 4; i++ {
		m.Once(context.Background())
	}
	if second.Contents()["README.md"] != "# Changed upstream" {
		t.Fatal("connected source change not confirmed")
	}
	if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "two", DestinationID: "one", Enabled: &enabled, ExpectedRevision: &revision}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("cycle accepted", err)
	}
	first.Modify(func() { first.Private = false })
	m.Once(context.Background())
	if s.mirrors()[0].State != "error" {
		t.Fatal("source visibility change not rejected")
	}
}

func TestConnectedMirrorRejectsUnverifiedAndPartialSource(t *testing.T) {
	for _, condition := range []string{"paused", "unverified", "partial", "same-repository"} {
		t.Run(condition, func(t *testing.T) {
			s, _, _, _ := connectedMoveFixtures(t)
			s.mutate(func(db *domain.Database) {
				src := db.Sources["one"]
				c := db.Connections[src.ConnectionID]
				switch condition {
				case "paused":
					c.Paused = true
				case "unverified":
					c.Verification = "unknown"
				case "partial":
					src.ImportPartial = true
				case "same-repository":
					c.RepositoryID = db.Connections["two"].RepositoryID
					c.Branch = db.Connections["two"].Branch
				}
				db.Sources["one"] = src
				db.Connections[src.ConnectionID] = c
			})
			enabled, revision := true, 0
			if _, err := s.configureMirror(context.Background(), MirrorRequest{SourceID: "one", DestinationID: "two", Enabled: &enabled, ExpectedRevision: &revision}); !errors.Is(err, domain.ErrConflict) {
				t.Fatal("unsafe source accepted", err)
			}
			if _, err := s.reconcileSources(context.Background(), ReconcileRequest{SourceID: "one", DestinationID: "two"}); !errors.Is(err, domain.ErrConflict) {
				t.Fatal("unsafe source reconciled", err)
			}
		})
	}
}

func TestMirrorCycleIncludesLongerChains(t *testing.T) {
	db := domain.Database{SourceMirrors: map[string]domain.SourceMirror{
		"b": {SourceID: "a", Enabled: true},
		"c": {SourceID: "b", Enabled: true},
	}}
	if !mirrorWouldCycle(&db, "c", "a") || mirrorWouldCycle(&db, "a", "c") {
		t.Fatal("invalid cycle detection")
	}
}

func TestConnectedReconcileBlocksUnobservedLinkedArticle(t *testing.T) {
	s, m, first, _ := connectedMoveFixtures(t)
	files := first.Contents()
	files["new.md"] = "[Linked](linked.md)"
	files["linked.md"] = "# Linked"
	first.Advance(files)
	m.Once(context.Background())
	s.mutate(func(db *domain.Database) {
		linked := db.Articles["one/linked.md"]
		linked.UpstreamHash = ""
		db.Articles["one/linked.md"] = linked
	})
	r, err := s.reconcileSources(context.Background(), ReconcileRequest{SourceID: "one", DestinationID: "two"})
	if err != nil || !containsPath(r.Blocked, "new.md") || !containsPath(r.Blocked, "linked.md") {
		t.Fatal("unobserved linked revision accepted", r, err)
	}
	if _, ok := s.db().Articles["two/new.md"]; ok {
		t.Fatal("linked graph copied despite unobserved revision")
	}
}

func TestConnectedMirrorSQLiteQueueAndDivergence(t *testing.T) {
	s, m, first, second := connectedMoveFixtures(t)
	files := second.Contents()
	files["README.md"] = "# Source"
	second.Advance(files)
	m.Once(context.Background())
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	m = newSyncManager(s, m.github)
	enabled, revision := true, 0
	if _, err = s.configureMirror(context.Background(), MirrorRequest{SourceID: "one", DestinationID: "two", Enabled: &enabled, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	files = first.Contents()
	files["README.md"] = "# First update"
	first.Advance(files)
	m.Once(context.Background())
	if len(s.jobs()) != 1 || s.jobs()[0].State != "pending" {
		t.Fatal("mirror update not queued")
	}
	s, err = s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	m = newSyncManager(s, m.github)
	for i := 0; i < 3; i++ {
		m.Once(context.Background())
	}
	if second.Contents()["README.md"] != "# First update" || s.jobs()[0].State != "complete" {
		t.Fatal("queued update did not survive reopen")
	}
	one := first.Contents()
	one["README.md"] = "# First independent"
	two := second.Contents()
	two["README.md"] = "# Second independent"
	first.Advance(one)
	second.Advance(two)
	m.Once(context.Background())
	if s.mirrors()[0].State != "blocked" || first.Contents()["README.md"] != "# First independent" || second.Contents()["README.md"] != "# Second independent" || len(s.jobs()) != 1 {
		t.Fatal("independent changes were not preserved")
	}
}
