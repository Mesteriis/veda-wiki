package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopyLinkedGraphToGitHubOneCommit(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "[B](b.md) ![icon](img/icon.png)", 0)
	s.put("from", "b.md", "[A](a.md)", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.db.Version != 9 || len(s.jobs()) != 1 || len(s.jobs()[0].Bundle) != 1 {
		t.Fatal("bundle not persisted")
	}
	if _, err = openStore(s.dir); err != nil {
		t.Fatal("cannot reopen bundle", err)
	}
	m.Once(context.Background())
	if f.writeCount() != 1 || f.contents()["a.md"] == "" || f.contents()["b.md"] == "" || f.contents()["img/icon.png"] == "" {
		t.Fatal("bundle not committed together")
	}
	if s.jobs()[0].State == "complete" {
		t.Fatal("completed before pull")
	}
	m.Once(context.Background())
	if s.jobs()[0].State != "complete" || s.db.Articles["from/a.md"].Deleted {
		t.Fatal("bundle not observed or source lost", s.jobs())
	}
}

func TestLinkedBundleRequiresCompleteObservation(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	job := s.jobs()[0]
	files := map[string]string{"a.md": "[B](b.md)", "b.md": "# B"}
	db := s.clone()
	if observedWrite(&db, "handbook", "a.md", job.SHA, "fixture", nil, files, false) {
		t.Fatal("partial pull completed bundle")
	}
	delete(files, "b.md")
	if observedWrite(&db, "handbook", "a.md", job.SHA, "fixture", nil, files, true) {
		t.Fatal("missing linked article completed bundle")
	}
}

func TestOldBinaryRejectsLinkedBundleStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V9_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(s.dir, "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(out), "unsupported store") {
		t.Fatal("old binary did not reject v9")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(before) != string(after) {
		t.Fatal("old binary changed v9 state")
	}
}

func TestLinkedBundleCancellationAndLocks(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	id := s.jobs()[0].ID
	if _, err = s.put("handbook", "b.md", "# Changed", 1); err != ErrConflict {
		t.Fatal("bundle edit accepted", err)
	}
	if err = s.cancelBundle(id, false); err != ErrInvalid {
		t.Fatal("cancel lacked confirmation", err)
	}
	if err = s.cancelBundle(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("handbook", "b.md", "# Changed", 1); err != nil {
		t.Fatal("cancel did not release lock", err)
	}
}

func TestLinkedBundleRecoversLostResponse(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	f.modify(func() { f.loseResponse = true })
	m.Once(context.Background())
	if f.writeCount() != 1 || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("missing prepared commit")
	}
	if err = s.cancelBundle(s.jobs()[0].ID, true); err != ErrConflict {
		t.Fatal("uncertain bundle cancelled", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(reopened, f.client()).Once(context.Background())
	if f.writeCount() != 1 || reopened.jobs()[0].State != "complete" {
		t.Fatal("bundle was not recovered", reopened.jobs())
	}
}

func TestLinkedBundleDoesNotOverwriteConcurrentRemoteArticle(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	f.advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "b.md": "# Concurrent"})
	m.Once(context.Background())
	if f.writeCount() != 0 || f.contents()["b.md"] != "# Concurrent" || f.contents()["a.md"] != "" || s.db.Articles["from/a.md"].Deleted {
		t.Fatal("concurrent remote article was overwritten")
	}
}
