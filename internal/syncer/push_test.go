package syncer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func TestStaleSyncCannotChangeOutboxAfterReconfiguration(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	m := connectFixture(t, s, f, false)
	if _, err := s.put("handbook", "new.md", "# New", 0); err != nil {
		t.Fatal(err)
	}
	old := s.connections()[0]
	if err := s.pauseConnection(old.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.pauseConnection(old.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.pushPending(context.Background(), old, "", "", ""); err != domain.ErrConflict {
		t.Fatal("stale cycle was accepted", err)
	}
	jobs := s.jobs()
	if len(jobs) != 1 || jobs[0].State != "pending" || jobs[0].LastError != "" {
		t.Fatal("stale cycle changed outbox")
	}
	current := s.connections()[0]
	if err := m.pushPending(context.Background(), current, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if s.jobs()[0].State != "blocked" {
		t.Fatal("current cycle did not update outbox")
	}
}

func TestGitHubWriteThroughAndRestartDurability(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Original", "src/main.go": "package original"})
	_ = connectFixture(t, s, f, true)
	if _, e := s.put("handbook", "docs/guide.md", "# Saved locally", 1); e != nil {
		t.Fatal(e)
	}
	if len(s.jobs()) != 1 {
		t.Fatal("write and outbox not atomic")
	}
	reopened, e := s.reopen()
	if e != nil {
		t.Fatal(e)
	}
	m := newSyncManager(reopened, f.Client())
	m.Once(context.Background())
	if f.Contents()["docs/guide.md"] != "# Saved locally" || f.Contents()["src/main.go"] != "package original" || f.Force {
		t.Fatal("unsafe remote write")
	}
	job := reopened.jobs()[0]
	if job.State != "prepared" || job.PreparedCommit == "" || job.ObservedCommit != "" {
		t.Fatal("write response must not be reported as a subsequent observation", job)
	}
	reopened, e = s.reopen()
	if e != nil {
		t.Fatal(e)
	}
	if reopened.jobs()[0].ObservedCommit != "" || reopened.db().Outbox[job.ID].ObservedCommit != "" {
		t.Fatal("unconfirmed observation after restart", e)
	}
	m = newSyncManager(reopened, f.Client())
	m.Once(context.Background())
	if reopened.jobs()[0].State != "complete" || reopened.jobs()[0].ObservedCommit == "" {
		t.Fatal(reopened.jobs())
	}
}

func TestGitHubLostResponseRecoversWithoutDuplicatePush(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Durable", 1)
	f.Modify(func() { f.LoseResponse = true })
	m.Once(context.Background())
	if f.WriteCount() != 1 || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("prepared write not durable")
	}
	reopened, e := s.reopen()
	if e != nil {
		t.Fatal(e)
	}
	reopened.retryConnection("handbook")
	newSyncManager(reopened, f.Client()).Once(context.Background())
	if f.WriteCount() != 1 || reopened.jobs()[0].State != "complete" {
		t.Fatal("ambiguous push duplicated or lost", reopened.jobs())
	}
}

func TestLegacyUnconfirmedObservedCommitIsNotReported(t *testing.T) {
	s := testStore(t)
	s.mutate(func(db *domain.Database) {
		db.Outbox = map[string]domain.SyncJob{}
		db.Outbox["fixture"] = domain.SyncJob{ID: "fixture", State: "prepared", ObservedCommit: strings.Repeat("a", 40)}
	})
	if got := s.jobs(); len(got) != 1 || got[0].ObservedCommit != "" {
		t.Fatal("legacy unconfirmed commit was exposed as an observation")
	}
}

func TestLaterEditRetainsUncertainWriteUntilObserved(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) { testLaterEditRetainsUncertainWrite(t, backend == "sqlite") })
	}
}

func testLaterEditRetainsUncertainWrite(t *testing.T, sqlite bool) {
	t.Helper()
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
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
	if _, err := s.put("handbook", "README.md", "# First edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	m.Once(context.Background())
	if jobs := s.jobs(); len(jobs) != 1 || jobs[0].State != "retry" || jobs[0].PreparedCommit == "" || f.WriteCount() != 1 {
		t.Fatal("fixture did not create an uncertain write", jobs)
	}
	if _, err := s.put("handbook", "README.md", "# Second edit", 2); err != nil {
		t.Fatal(err)
	}
	jobs := s.jobs()
	if len(jobs) != 2 || jobs[0].State != "retry" || jobs[0].PreparedCommit == "" || jobs[1].State != "pending" {
		t.Fatal("later edit discarded uncertain write", jobs)
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m = newSyncManager(reopened, f.Client())
	m.Once(context.Background())
	m.Once(context.Background())
	jobs = reopened.jobs()
	if len(jobs) != 2 || jobs[0].State != "complete" || jobs[1].State != "complete" || f.WriteCount() != 2 {
		t.Fatal("writes were not independently confirmed", jobs, f.WriteCount())
	}
	if got := f.Contents()["README.md"]; got != "# Second edit" {
		t.Fatal("latest edit not written")
	}
}

func TestLaterEditCannotOvertakeDeferredPreparedWrite(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original", "other.md": "# Other"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "README.md", "# First edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = true })
	m.Once(context.Background())
	if jobs := s.jobs(); len(jobs) != 1 || jobs[0].PreparedCommit == "" || jobs[0].State != "retry" {
		t.Fatal("fixture write not deferred", jobs)
	}
	if _, err := s.put("handbook", "README.md", "# Second edit", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "other.md", "# Other edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = false })
	// Force a long retry delay so only an incorrect later-job dispatch can write.
	s.mutate(func(db *domain.Database) {
		first := db.Outbox["handbook/README.md@2"]
		first.NextAttempt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
		db.Outbox[first.ID] = first
	})
	if err := m.pushPending(context.Background(), s.connections()[0], f.Head, f.Commits[f.Head].Tree, githubtest.Access); err != nil {
		t.Fatal(err)
	}
	if f.WriteCount() != 1 || f.Contents()["README.md"] != "# Original" || f.Contents()["other.md"] != "# Other edit" {
		t.Fatal("later revision overtook prepared write or unrelated article stalled")
	}
}

func TestConflictResolutionDoesNotDiscardPreparedWrite(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) { testConflictResolutionDoesNotDiscardPreparedWrite(t, backend == "sqlite") })
	}
}

func testConflictResolutionDoesNotDiscardPreparedWrite(t *testing.T, sqlite bool) {
	t.Helper()
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
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
	if _, err := s.put("handbook", "README.md", "# Local", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = true })
	m.Once(context.Background())
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Different upstream"})
	if err := s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	conflicts, jobs := s.conflicts(), s.jobs()
	if len(conflicts) != 1 || len(jobs) != 1 || jobs[0].PreparedCommit == "" {
		t.Fatal("fixture lacks conflict and prepared write", conflicts, jobs)
	}
	for _, choice := range []string{"local", "upstream"} {
		if err := s.resolveConflict("handbook", "README.md", conflicts[0].Index, 2, choice); !errors.Is(err, domain.ErrConflict) {
			t.Fatal("uncertain write was discarded", choice, err)
		}
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.conflicts()) != 1 || reopened.jobs()[0].PreparedCommit == "" || reopened.jobs()[0].State == "superseded" {
		t.Fatal("uncertain write or conflict lost after restart")
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Local"})
	if err := reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(reopened, f.Client()).Once(context.Background())
	if len(reopened.conflicts()) != 0 || reopened.jobs()[0].State != "complete" {
		t.Fatal("confirmed prepared content did not reconcile", reopened.jobs())
	}
}

func TestExternalMatchForLaterEditDoesNotDiscardOlderUncertainWrite(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "README.md", "# First edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = true })
	m.Once(context.Background())
	if _, err := s.put("handbook", "README.md", "# Second edit", 2); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = false })
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Second edit"})
	if err := s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	jobs := s.jobs()
	if len(jobs) != 2 || jobs[0].PreparedCommit == "" || jobs[0].State == "superseded" || jobs[0].State == "complete" || jobs[1].State == "complete" {
		t.Fatal("later external content hid uncertain write", jobs)
	}
	if len(s.conflicts()) != 1 || f.WriteCount() != 0 || f.Contents()["README.md"] != "# Second edit" {
		t.Fatal("uncertain write did not preserve external content")
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.conflicts()) != 1 || reopened.jobs()[0].PreparedCommit == "" {
		t.Fatal("uncertainty lost after restart")
	}
}

func TestIdenticalLaterEditIsSatisfiedWithoutDuplicatePush(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	if _, err := s.put("handbook", "README.md", "# Same edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	m.Once(context.Background())
	if _, err := s.put("handbook", "README.md", "# Same edit", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	jobs := s.jobs()
	if len(jobs) != 2 || jobs[0].State != "complete" || jobs[1].State != "complete" || f.WriteCount() != 1 {
		t.Fatal("identical revision caused duplicate push or lost confirmation", jobs, f.WriteCount())
	}
}

func TestIdenticalLaterEditRequiresItsOwnDependencies(t *testing.T) {
	hash := domain.Digest([]byte("# Same edit"))
	db := domain.Database{Outbox: map[string]domain.SyncJob{
		"first": {ID: "first", SourceID: "handbook", Path: "README.md", Revision: 2, SHA: hash, State: "retry", PreparedCommit: strings.Repeat("a", 40)},
		"later": {ID: "later", SourceID: "handbook", Path: "README.md", Revision: 3, SHA: hash, State: "pending", Assets: map[string]domain.BlobRef{"image.png": {SHA: strings.Repeat("b", 64), MIME: "image/png"}}},
	}}
	if !domain.ObservedWrite(&db, "handbook", "README.md", hash, strings.Repeat("c", 40), nil, nil, true) {
		t.Fatal("prepared write was not observed")
	}
	if db.Outbox["first"].State != "complete" || db.Outbox["later"].State != "pending" {
		t.Fatal("later write confirmed without its image")
	}
}

func TestGitHubCompareAndSwapNeverForcesUpdates(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Local", 1)
	f.Modify(func() { f.Reject = true })
	m.Once(context.Background())
	if f.WriteCount() != 0 || s.jobs()[0].State != "retry" || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("CAS refusal lost work", s.jobs())
	}
	p, _ := s.page("handbook/README.md", false)
	if p.Text != "# Local" {
		t.Fatal("local write lost")
	}
	f.Modify(func() { f.Reject = false })
	s.retryConnection("handbook")
	m.Once(context.Background())
	m.Once(context.Background())
	if f.WriteCount() != 1 || s.jobs()[0].State != "complete" {
		t.Fatal(s.jobs())
	}
}

func TestGitHubEnableWritesPreservesQueuedLocalArticles(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Original"})
	m := connectFixture(t, s, f, false)
	s.put("handbook", "docs/guide.md", "# Local", 1)
	m.Once(context.Background())
	if s.jobs()[0].State != "blocked" || f.WriteCount() != 0 {
		t.Fatal("read-only connection wrote remotely")
	}
	if _, e := s.saveAccount(context.Background(), m.github, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); e != nil {
		t.Fatal(e)
	}
	account, write, expected := "operator", true, 1
	c, e := m.Configure(context.Background(), ConfigureRequest{ID: "handbook", AccountID: &account, Write: &write, Expected: &expected})
	if e != nil || c.ConfigRevision != 2 {
		t.Fatal(c, e)
	}
	if _, e := m.Configure(context.Background(), ConfigureRequest{ID: "handbook", Write: &write, Expected: &expected}); e != domain.ErrConflict {
		t.Fatal("stale configuration accepted", e)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.WriteCount() != 1 || s.jobs()[0].State != "complete" {
		t.Fatal(s.jobs())
	}
}
