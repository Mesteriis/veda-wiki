package main

import (
	"context"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.DB().Version != 9 || len(s.jobs()) != 1 || len(s.jobs()[0].Bundle) != 1 {
		t.Fatal("bundle not persisted")
	}
	if _, err = openStore(s.Dir()); err != nil {
		t.Fatal("cannot reopen bundle", err)
	}
	m.Once(context.Background())
	if f.WriteCount() != 1 || f.Contents()["a.md"] == "" || f.Contents()["b.md"] == "" || f.Contents()["img/icon.png"] == "" {
		t.Fatal("bundle not committed together")
	}
	if s.jobs()[0].State == "complete" {
		t.Fatal("completed before pull")
	}
	m.Once(context.Background())
	if s.jobs()[0].State != "complete" || s.DB().Articles["from/a.md"].Deleted {
		t.Fatal("bundle not observed or source lost", s.jobs())
	}
}

func TestCopyDirectoryReadmeToGitHubAsOneBundle(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "guide.md", "# Guide\n\n[Docs](docs/)", 0)
	s.put("from", "docs/README.md", "# Docs", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	_ = connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "guide.md", IncludeLinked: true, ConfirmPublication: true})
	if err != nil || s.DB().Version != 16 || len(s.jobs()) != 1 || len(s.jobs()[0].Bundle) != 1 {
		t.Fatal("directory bundle not queued", err)
	}
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal("directory bundle did not reopen", err)
	}
	m := newSyncManager(s, f.Client())
	m.Once(context.Background())
	if f.WriteCount() != 1 || f.Contents()["docs/README.md"] != "# Docs" || s.jobs()[0].State == "complete" {
		t.Fatal("directory bundle was not atomically sent")
	}
	m.Once(context.Background())
	if s.jobs()[0].State != "complete" {
		t.Fatal("directory bundle not confirmed")
	}
}

func TestIndexDirectoryBundleRejectsNewRemoteIndex(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "guide.md", "[Docs](docs/)", 0)
	s.put("from", "docs/index.md", "# Original index", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "guide.md", IncludeLinked: true, ConfirmPublication: true})
	if err != nil || s.DB().Version != 17 {
		t.Fatal("index bundle not queued", err)
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "docs/index.md": "# Independent index"})
	m.Once(context.Background())
	if f.WriteCount() != 0 || f.Contents()["docs/README.md"] != "" || s.jobs()[0].State == "complete" {
		t.Fatal("new remote index was shadowed")
	}
}

func TestIndexDirectoryBundleSentAndConfirmed(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "guide.md", "[Docs](docs/)", 0)
	s.put("from", "docs/index.md", "# Original index", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "guide.md", IncludeLinked: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if f.WriteCount() != 1 || f.Contents()["docs/README.md"] != "# Original index" {
		t.Fatal("mapped index not sent")
	}
	m.Once(context.Background())
	if s.jobs()[0].State != "complete" {
		t.Fatal("mapped index not confirmed")
	}
}

func TestIndexDirectoryMoveWaitsForDestinationConfirmation(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "guide.md", "[Docs](docs/)", 0)
	s.put("from", "docs/index.md", "# Original index", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "guide.md", IncludeLinked: true, ConfirmMove: true, ConfirmPublication: true})
	if err != nil || move.State != "waiting_destination" {
		t.Fatal("move not queued", err)
	}
	m.Once(context.Background())
	if s.DB().Articles["from/guide.md"].Deleted {
		t.Fatal("source removed before confirmation")
	}
	m.Once(context.Background())
	if s.moves()[0].State != "complete" || !s.DB().Articles["from/guide.md"].Deleted || s.DB().Articles["from/docs/index.md"].Deleted {
		t.Fatal("move did not preserve linked source after confirmation")
	}
}

func TestIndexDirectoryRootCannotShadowNewRemoteIndex(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "docs/index.md", "# Root\n\n[Self](./)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "docs/index.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "docs/README.md", IncludeLinked: true, ConfirmPublication: true})
	if err != nil || len(s.jobs()[0].Bundle) != 0 {
		t.Fatal("root fixture invalid", err)
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "docs/index.md": "# Independent index"})
	m.Once(context.Background())
	if f.WriteCount() != 0 || f.Contents()["docs/README.md"] != "" {
		t.Fatal("root copy shadowed new remote index")
	}
}

func TestOldBinaryRejectsDirectoryLinkStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V16_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "guide.md", "[Docs](docs/)", 0)
	s.put("from", "docs/README.md", "# Docs", 0)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", IncludeLinked: true}); err != nil {
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
		t.Fatal("old binary did not reject version 16")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(before) != string(after) {
		t.Fatal("old binary changed state")
	}
}

func TestOldBinaryRejectsDirectoryIndexStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V17_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "guide.md", "[Docs](docs/)", 0)
	s.put("from", "docs/index.md", "# Docs", 0)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", IncludeLinked: true}); err != nil {
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
		t.Fatal("old binary did not reject version 17")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(after) != string(before) {
		t.Fatal("old binary changed state")
	}
}

func TestLinkedBundleRequiresCompleteObservation(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	job := s.jobs()[0]
	files := map[string]string{"a.md": "[B](b.md)", "b.md": "# B"}
	db := s.Clone()
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true}); err != nil {
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	m.Once(context.Background())
	if f.WriteCount() != 1 || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("missing prepared commit")
	}
	if err = s.cancelBundle(s.jobs()[0].ID, true); err != ErrConflict {
		t.Fatal("uncertain bundle cancelled", err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(reopened, f.Client()).Once(context.Background())
	if f.WriteCount() != 1 || reopened.jobs()[0].State != "complete" {
		t.Fatal("bundle was not recovered", reopened.jobs())
	}
}

func TestLinkedBundleDoesNotOverwriteConcurrentRemoteArticle(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "b.md": "# Concurrent"})
	m.Once(context.Background())
	if f.WriteCount() != 0 || f.Contents()["b.md"] != "# Concurrent" || f.Contents()["a.md"] != "" || s.DB().Articles["from/a.md"].Deleted {
		t.Fatal("concurrent remote article was overwritten")
	}
}

func TestCopyRootDependenciesToGitHubInOneCommit(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	if _, err := s.put("from", "guide.md", "# Guide", 0); err != nil {
		t.Fatal(err)
	}
	text := "[Guide](/guide.md)\n\n![Icon](/img/icon.png)"
	if _, err := s.put("from", "chapter/a.md", text, 0); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	in := copyRequest{SourceID: "from", SourcePath: "chapter/a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "other/b.md", IncludeLinked: true}
	if _, err := s.copyArticle(in); err != ErrInvalid {
		t.Fatal("unconfirmed publication accepted", err)
	}
	in.ConfirmPublication = true
	if _, err := s.copyArticle(in); err != nil {
		t.Fatal(err)
	}
	if s.DB().Version != 12 {
		t.Fatal("root dependency bundle did not upgrade the store")
	}
	if _, err := openStore(s.Dir()); err != nil {
		t.Fatal("root dependency store did not reopen", err)
	}
	manager.Once(context.Background())
	if f.WriteCount() != 1 || f.Contents()["other/b.md"] != text || f.Contents()["guide.md"] != "# Guide" || f.Contents()["img/icon.png"] == "" {
		t.Fatal("root dependencies were not committed together")
	}
	manager.Once(context.Background())
	if s.jobs()[0].State != "complete" {
		t.Fatal("root bundle was not observed", s.jobs()[0].State)
	}
}

func TestOldBinaryRejectsRootDependencyStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V11_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	if _, err := s.put("from", "a.md", "![Icon](/img/icon.png)", 0); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", ConfirmPublication: true})
	if err != nil || s.DB().Version != 12 {
		t.Fatal("root dependency store not created", err)
	}
	state := filepath.Join(s.Dir(), "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, old, "--data", s.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(output), "unsupported store") {
		t.Fatal("old binary did not reject v12")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(before) != string(after) {
		t.Fatal("old binary changed v12 data")
	}
}
