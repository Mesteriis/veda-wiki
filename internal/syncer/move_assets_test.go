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
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

func TestOldBinaryRejectsImageMoveStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V6_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	if _, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true}); err != nil {
		t.Fatal(err)
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
		t.Fatal("old binary did not reject v6")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(before) != string(after) {
		t.Fatal("old binary changed v6 data")
	}
}

func TestMoveImageWaitsForVerifiedBlobBeforeRemoteDeletion(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "![icon](img/icon.png)", "img/icon.png": string(smallPNG(t))})
	manager := connectFixture(t, s, f, true)
	source(t, s, "to", "private")
	m, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/moved.md", ConfirmMove: true})
	if err != nil || m.State != "waiting_source_delete" {
		t.Fatal("move", m, err)
	}
	ref := m.Assets["nested/img/icon.png"]
	if err = os.WriteFile(filepath.Join(s.Dir(), "blobs", ref.SHA), []byte("corrupt synthetic image"), 0600); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.WriteCount() != 0 || f.Contents()["guide.md"] == "" {
		t.Fatal("source deleted despite corrupt image")
	}
	if err = s.verifyAsset(ref); err != nil {
		t.Fatal("source image was not repaired", err)
	}
	syncFixtureSource(t, manager, "handbook")
	if f.WriteCount() != 1 {
		t.Fatal("move did not resume after a fresh verification")
	}
}

func TestMoveImageFromConnectedSourceToLocal(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "![icon](img/icon.png)", "img/icon.png": string(smallPNG(t))})
	manager := connectFixture(t, s, f, true)
	source(t, s, "to", "private")
	m, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || f.WriteCount() != 1 {
		t.Fatal("remote move did not complete", s.moves())
	}
	if _, exists := f.Contents()["guide.md"]; exists {
		t.Fatal("remote source still exists")
	}
	if s.db().Sources["to"].Assets["nested/img/icon.png"] != m.Assets["nested/img/icon.png"] {
		t.Fatal("target image missing")
	}
}

func TestMoveImageDetectsChangedDestinationBeforeDeletion(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "![icon](img/icon.png)", "img/icon.png": string(smallPNG(t))})
	manager := connectFixture(t, s, f, true)
	source(t, s, "to", "private")
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	s.mutate(func(db *domain.Database) {
		dst := db.Sources["to"]
		dst.Assets = map[string]domain.BlobRef{}
		db.Sources["to"] = dst
		domain.ReconcileMoves(db)
	})
	manager.Once(context.Background())
	if s.moves()[0].State != "destination_changed" || f.WriteCount() != 0 || f.Contents()["guide.md"] == "" {
		t.Fatal("source removed after target image changed")
	}
}

func TestCopyImageQueuesBundleWithoutDeletingSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	_ = connectFixture(t, s, f, true)
	_, err := s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.db().Version != domain.RevisionsFormat || s.jobs()[0].Assets["img/icon.png"] != ref {
		t.Fatal("copy dependencies not persisted")
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	manager := newSyncManager(reopened, f.Client())
	manager.Once(context.Background())
	manager.Once(context.Background())
	if reopened.jobs()[0].State != "complete" || reopened.db().Articles["from/a.md"].Deleted || reopened.db().Sources["handbook"].Assets["img/icon.png"] != ref {
		t.Fatal("copy did not synchronize")
	}
}

func TestConnectedBundleUploadsIdenticalImageOnlyOnce(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	ref := fixtureAsset(t, s, "from", "img/one.png")
	s.mutate(func(db *domain.Database) {
		src := db.Sources["from"]
		src.Assets = map[string]domain.BlobRef{"img/one.png": ref, "img/two.png": ref}
		db.Sources[src.ID] = src
	})
	s.put("from", "a.md", "![one](img/one.png)\n![two](img/two.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	if _, err := s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	f.Mu.Lock()
	blobWrites := f.BlobWrites
	f.Mu.Unlock()
	if blobWrites != 2 || f.Contents()["img/one.png"] != f.Contents()["img/two.png"] || s.jobs()[0].State != "complete" {
		t.Fatal("identical image uploaded more than once or missing", blobWrites)
	}
}

func TestOldBinaryRejectsQueuedImageStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V7_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	if _, err := s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true}); err != nil {
		t.Fatal(err)
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
		t.Fatal("old binary did not reject v7")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(before) != string(after) {
		t.Fatal("old binary changed v7 data")
	}
}

func TestConnectedImageMoveRecoversLostResponse(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	manager.Once(context.Background())
	if f.WriteCount() != 1 || s.db().Articles["from/a.md"].Deleted {
		t.Fatal("lost response removed source")
	}
	if err = s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.WriteCount() != 1 || s.moves()[0].State != "complete" {
		t.Fatal("lost response not recovered", s.moves())
	}
}

func TestMoveWaitsWhenRemoteArticleExistsButImageIsMissing(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "b.md": "![icon](img/icon.png)"})
	manager.Once(context.Background())
	if s.db().Articles["from/a.md"].Deleted || s.moves()[0].State != "waiting_destination" {
		t.Fatal("source deleted before image was observed")
	}
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || f.Contents()["img/icon.png"] == "" {
		t.Fatal("missing image did not recover", s.moves())
	}
}

func TestConnectedImageMoveRejectsRemoteCollision(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "img/icon.png": "unrelated remote content"})
	manager.Once(context.Background())
	if f.WriteCount() != 0 || f.Contents()["img/icon.png"] != "unrelated remote content" || s.db().Articles["from/a.md"].Deleted {
		t.Fatal("remote image overwritten")
	}
}

func TestEditedPendingCopyKeepsReferencedAssets(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.put("handbook", "b.md", "# Updated\n\n![icon](img/icon.png)", 1)
	if err != nil {
		t.Fatal(err)
	}
	if s.moves()[0].State != "destination_changed" || s.db().Outbox["handbook/b.md@2"].Assets["img/icon.png"] != ref {
		t.Fatal("pending dependency lost after edit")
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.db().Articles["from/a.md"].Deleted || domain.Digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA {
		t.Fatal("edit lost the image or original")
	}
}

func TestEditedPreparedBundleUpdatesFromObservedBase(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	if _, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.WriteCount() != 1 || s.db().Articles["from/a.md"].Deleted {
		t.Fatal("initial bundle not pending")
	}
	if _, err := s.put("handbook", "b.md", "# Updated\n![icon](img/icon.png)", 1); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.db().Articles["from/a.md"].Deleted || f.Contents()["b.md"] != "# Updated\n![icon](img/icon.png)" || domain.Digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA {
		t.Fatal("prepared bundle edit did not synchronize safely")
	}
}

func TestConcurrentEditCannotReviveSupersededBundle(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	if _, err := s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	hookErr := make(chan error, 1)
	f.Modify(func() {
		f.AfterCommit = func() { _, err := s.put("handbook", "b.md", "# New\n![icon](img/icon.png)", 1); hookErr <- err }
	})
	manager.Once(context.Background())
	select {
	case err := <-hookErr:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("commit hook was not called")
	}
	if f.WriteCount() != 0 || s.db().Outbox["handbook/b.md@1"].State != "superseded" || s.db().Outbox["handbook/b.md@2"].Assets["img/icon.png"] != ref {
		t.Fatal("superseded bundle was revived")
	}
}

func TestEditedPendingCopyDoesNotPublishPrivateAssets(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	s.mutate(func(db *domain.Database) {
		c := db.Connections["handbook"]
		c.VerifiedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
		db.Connections[c.ID] = c
	})
	_, err := s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md"})
	if err != nil {
		t.Fatal(err)
	}
	s.mutate(func(db *domain.Database) {
		c := db.Connections["handbook"]
		c.VerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
		db.Connections[c.ID] = c
	})
	_, err = s.put("handbook", "b.md", "# Updated\n![icon](img/icon.png)", 1)
	if err != nil {
		t.Fatal(err)
	}
	job := s.db().Outbox["handbook/b.md@2"]
	if job.Visibility != "private" || len(job.Assets) != 1 {
		t.Fatal("unapproved asset became public")
	}
	manager.Once(context.Background())
	if f.WriteCount() != 0 {
		t.Fatal("unapproved asset pushed to public repository")
	}
}

func TestEditedPendingCopyDoesNotSendRemovedImages(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	if _, err := s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "b.md", "# No image", 1); err != nil {
		t.Fatal(err)
	}
	if len(s.db().Outbox["handbook/b.md@2"].Assets) != 0 {
		t.Fatal("removed image retained")
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if _, exists := f.Contents()["img/icon.png"]; exists {
		t.Fatal("removed image was published")
	}
}

func TestImageMoveDoesNotPublishToPartialTarget(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "broken.png": "not an image"})
	manager := connectFixture(t, s, f, true)
	if !s.db().Sources["handbook"].ImportPartial {
		t.Fatal("fixture must be partial")
	}
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.WriteCount() != 0 || s.db().Articles["from/a.md"].Deleted {
		t.Fatal("partial target received write or lost source")
	}
}

func TestImageBundleHonorsRepositoryFileLimit(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "handbook", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	files := map[string]string{}
	for i := 0; i < 2000; i++ {
		files[fmt.Sprintf("docs/%d.md", i)] = "# Existing"
	}
	f := githubtest.New(t, files)
	manager := newSyncManager(s, f.Client())
	head, err := manager.github.Head(context.Background(), f.Name, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := manager.github.Tree(context.Background(), f.Name, head, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.pendingBundle(context.Background(), domain.GitHubConnection{ID: "handbook", Repository: f.Name, Branch: "main"}, head, tree.SHA, "", domain.SyncJob{Path: "new.md", Revision: 1, Assets: map[string]domain.BlobRef{"img/icon.png": ref}}, domain.Article{Versions: []domain.Revision{{Number: 1, Text: "![icon](img/icon.png)"}}})
	var remote *github.RemoteError
	if !errors.As(err, &remote) || remote.Code != "repository_limit" || f.WriteCount() != 0 {
		t.Fatal("repository limit not enforced", err)
	}
}

func TestImageMoveBetweenConnectedRepositories(t *testing.T) {
	s, manager, original, destination := connectedMoveFixtures(t)
	image := smallPNG(t)
	original.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source\n\n![icon](img/icon.png)", "img/icon.png": string(image)})
	syncFixtureSource(t, manager, "one")
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 2, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_destination" {
		t.Fatal("move setup", move, err)
	}
	syncFixtureSource(t, manager, "two")
	syncFixtureSource(t, manager, "one")
	if original.Contents()["README.md"] == "" || s.moves()[0].State != "waiting_destination" {
		t.Fatal("source removed before destination observation")
	}
	if destination.WriteCount() != 1 || destination.Contents()["img/icon.png"] != string(image) {
		t.Fatal("image not committed with article")
	}
	syncFixtureSource(t, manager, "two")
	if s.moves()[0].State != "waiting_source_delete" {
		t.Fatal("destination bundle not observed", s.moves())
	}
	syncFixtureSource(t, manager, "one")
	syncFixtureSource(t, manager, "one")
	if s.moves()[0].State != "complete" || original.Contents()["README.md"] != "" || destination.Contents()["img/icon.png"] != string(image) {
		t.Fatal("connected image move incomplete", s.moves())
	}
}

func TestImageTransferRejectsImportedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	b := testBatch()
	b.SourceID = "imported"
	b.Visibility = "private"
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "imported", DestinationPath: "b.md", ConfirmMove: true})
	if err != domain.ErrInvalid || s.db().Articles["from/a.md"].Deleted {
		t.Fatal("imported destination accepted", err)
	}
}
