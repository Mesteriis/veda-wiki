package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureAsset(t *testing.T, s *Store, sourceID, assetPath string) BlobRef {
	t.Helper()
	image := smallPNG(t)
	ref, err := imageRef(image)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(s.Dir(), "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Dir(), "blobs", ref.SHA), image, 0600); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	defer s.Unlock()
	db := s.Clone()
	src := db.Sources[sourceID]
	src.Assets = map[string]BlobRef{assetPath: ref}
	db.Sources[sourceID] = src
	if err = s.Commit(db); err != nil {
		t.Fatal(err)
	}
	return ref
}

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
	if _, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true}); err != nil {
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

func TestMoveCopiesRelativeImageToLocalDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	ref := fixtureAsset(t, s, "from", "guide/img/icon.png")
	if _, err := s.put("from", "guide/a.md", "# Guide\n\n![icon](img/icon.png)", 0); err != nil {
		t.Fatal(err)
	}
	m, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "guide/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/b.md", ConfirmMove: true})
	if err != nil || m.State != "complete" || m.Assets["nested/img/icon.png"] != ref {
		t.Fatal("move failed", m, err)
	}
	if s.DB().Sources["to"].Assets["nested/img/icon.png"] != ref {
		t.Fatal("target image missing")
	}
	if _, err := s.createAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.DB().Version != 6 || reopened.moves()[0].Assets["nested/img/icon.png"] != ref {
		t.Fatal("move did not survive restart", err)
	}
	w := Wiki{Store: reopened, Token: "test-owner"}
	r := request(w, "GET", "/media/to/nested/img/icon.png", "", true)
	if r.Code != 200 {
		t.Fatal("image unavailable", r.Code)
	}
}

func TestMoveImageAndTranslationTogether(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "from", "private")
	programSource(t, s, "to", "private")
	ref := fixtureAsset(t, s, "from", "docs/img/icon.png")
	rev, err := s.put("from", "docs/a.md", "# Original\n\n![icon](img/icon.png)", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.submitTranslation(translationRequest{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод\n\n![значок](img/icon.png)"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.moveArticle(moveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/sub/b.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.createAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.DB().Version != 6 {
		t.Fatal("v6 restart failed", err)
	}
	page, err := reopened.page("to/docs/sub/b.md", false)
	if err != nil || !page.Translated || reopened.DB().Sources["to"].Assets["docs/sub/img/icon.png"] != ref {
		t.Fatal("translation or image lost", err)
	}
}

func TestCopyImageRejectsCollisionAndPreservesSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md"}); err != nil {
		t.Fatal(err)
	}
	if s.DB().Sources["to"].Assets["img/icon.png"] != ref || s.DB().Articles["from/a.md"].Deleted {
		t.Fatal("copy lost data")
	}
	s.put("from", "b.md", "![icon](img/icon.png)", 0)
	db := s.Clone()
	dst := db.Sources["to"]
	dst.Assets = map[string]BlobRef{"img/icon.png": {SHA: strings.Repeat("a", 64), MIME: "image/png"}}
	db.Sources["to"] = dst
	s.Lock()
	*s.DB() = db
	s.Unlock()
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "b.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"})
	if err != ErrConflict {
		t.Fatal("collision accepted", err)
	}
	if _, exists := s.DB().Articles["to/b.md"]; exists {
		t.Fatal("failed copy committed")
	}
}

func TestMoveImageWaitsForVerifiedBlobBeforeRemoteDeletion(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "![icon](img/icon.png)", "img/icon.png": string(smallPNG(t))})
	manager := connectFixture(t, s, f, true)
	source(t, s, "to", "private")
	m, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/moved.md", ConfirmMove: true})
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
	m, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/moved.md", ConfirmMove: true})
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
	if s.DB().Sources["to"].Assets["nested/img/icon.png"] != m.Assets["nested/img/icon.png"] {
		t.Fatal("target image missing")
	}
}

func TestMoveImageDetectsChangedDestinationBeforeDeletion(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "![icon](img/icon.png)", "img/icon.png": string(smallPNG(t))})
	manager := connectFixture(t, s, f, true)
	source(t, s, "to", "private")
	_, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	s.Lock()
	db := s.Clone()
	dst := db.Sources["to"]
	dst.Assets = map[string]BlobRef{}
	db.Sources["to"] = dst
	reconcileMoves(&db)
	if err = s.Commit(db); err != nil {
		s.Unlock()
		t.Fatal(err)
	}
	s.Unlock()
	manager.Once(context.Background())
	if s.moves()[0].State != "destination_changed" || f.WriteCount() != 0 || f.Contents()["guide.md"] == "" {
		t.Fatal("source removed after target image changed")
	}
}

func TestPublicImageCopyRequiresPublicationConfirmation(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "public")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	in := copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"}
	if _, err := s.copyArticle(in); err != ErrInvalid {
		t.Fatal("private image published without confirmation", err)
	}
	if len(s.DB().Sources["to"].Assets) != 0 {
		t.Fatal("refused image published")
	}
	in.ConfirmPublication = true
	if _, err := s.copyArticle(in); err != nil {
		t.Fatal(err)
	}
	if s.DB().Sources["to"].Assets["img/icon.png"] != ref {
		t.Fatal("confirmed image missing")
	}
	if r := request(Wiki{Store: s}, "GET", "/media/to/img/icon.png", "", false); r.Code != 200 {
		t.Fatal("confirmed public image unavailable", r.Code)
	}
}

func TestCopyRejectsUnresolvedLinksAndRawHTML(t *testing.T) {
	for _, text := range []string{"[guide](other.md)", "<img src=\"local.png\">", "<a href=\"other.md\">link</a>"} {
		s := testStore(t)
		source(t, s, "from", "private")
		source(t, s, "to", "private")
		s.put("from", "a.md", text, 0)
		if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"}); err != ErrInvalid {
			t.Fatal("unresolved dependency accepted", err)
		}
	}
}

func TestImageTransferToConnectedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	m, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil || m.State != "waiting_destination" || s.DB().Version != 7 {
		t.Fatal("move not queued", err, m)
	}
	if r := request(Wiki{Store: s}, "GET", "/media/handbook/img/icon.png", "", false); r.Code != 404 {
		t.Fatal("pending image exposed", r.Code)
	}
	manager.Once(context.Background())
	if s.DB().Articles["from/a.md"].Deleted || f.WriteCount() != 1 || f.Contents()["b.md"] == "" || digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA {
		t.Fatal("bundle was not committed atomically")
	}
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || !s.DB().Articles["from/a.md"].Deleted || s.DB().Sources["handbook"].Assets["img/icon.png"] != ref {
		t.Fatal("observed bundle did not finish move", s.moves())
	}
	if _, err := s.createAccessKey("agent", []string{"handbook"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.DB().Version != 7 {
		t.Fatal("queued assets did not persist", err)
	}
}

func TestCopyImageQueuesBundleWithoutDeletingSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	_ = connectFixture(t, s, f, true)
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.DB().Version != 7 || s.jobs()[0].Assets["img/icon.png"] != ref {
		t.Fatal("copy dependencies not persisted")
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	manager := newSyncManager(reopened, f.Client())
	manager.Once(context.Background())
	manager.Once(context.Background())
	if reopened.jobs()[0].State != "complete" || reopened.DB().Articles["from/a.md"].Deleted || reopened.DB().Sources["handbook"].Assets["img/icon.png"] != ref {
		t.Fatal("copy did not synchronize")
	}
}

func TestConnectedBundleUploadsIdenticalImageOnlyOnce(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	ref := fixtureAsset(t, s, "from", "img/one.png")
	s.Lock()
	db := s.Clone()
	src := db.Sources["from"]
	src.Assets = map[string]BlobRef{"img/one.png": ref, "img/two.png": ref}
	db.Sources[src.ID] = src
	if err := s.Commit(db); err != nil {
		s.Unlock()
		t.Fatal(err)
	}
	s.Unlock()
	s.put("from", "a.md", "![one](img/one.png)\n![two](img/two.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true}); err != nil {
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
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true}); err != nil {
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
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	manager.Once(context.Background())
	if f.WriteCount() != 1 || s.DB().Articles["from/a.md"].Deleted {
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
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "b.md": "![icon](img/icon.png)"})
	manager.Once(context.Background())
	if s.DB().Articles["from/a.md"].Deleted || s.moves()[0].State != "waiting_destination" {
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
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "img/icon.png": "unrelated remote content"})
	manager.Once(context.Background())
	if f.WriteCount() != 0 || f.Contents()["img/icon.png"] != "unrelated remote content" || s.DB().Articles["from/a.md"].Deleted {
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
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.put("handbook", "b.md", "# Updated\n\n![icon](img/icon.png)", 1)
	if err != nil {
		t.Fatal(err)
	}
	if s.moves()[0].State != "destination_changed" || s.DB().Outbox["handbook/b.md@2"].Assets["img/icon.png"] != ref {
		t.Fatal("pending dependency lost after edit")
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.DB().Articles["from/a.md"].Deleted || digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA {
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
	if _, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.WriteCount() != 1 || s.DB().Articles["from/a.md"].Deleted {
		t.Fatal("initial bundle not pending")
	}
	if _, err := s.put("handbook", "b.md", "# Updated\n![icon](img/icon.png)", 1); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.DB().Articles["from/a.md"].Deleted || f.Contents()["b.md"] != "# Updated\n![icon](img/icon.png)" || digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA {
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
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true}); err != nil {
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
	if f.WriteCount() != 0 || s.DB().Outbox["handbook/b.md@1"].State != "superseded" || s.DB().Outbox["handbook/b.md@2"].Assets["img/icon.png"] != ref {
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
	s.Lock()
	c := s.DB().Connections["handbook"]
	c.VerifiedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
	s.DB().Connections[c.ID] = c
	s.Unlock()
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md"})
	if err != nil {
		t.Fatal(err)
	}
	s.Lock()
	c = s.DB().Connections["handbook"]
	c.VerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.DB().Connections[c.ID] = c
	s.Unlock()
	_, err = s.put("handbook", "b.md", "# Updated\n![icon](img/icon.png)", 1)
	if err != nil {
		t.Fatal(err)
	}
	job := s.DB().Outbox["handbook/b.md@2"]
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
	if _, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("handbook", "b.md", "# No image", 1); err != nil {
		t.Fatal(err)
	}
	if len(s.DB().Outbox["handbook/b.md@2"].Assets) != 0 {
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
	if !s.DB().Sources["handbook"].ImportPartial {
		t.Fatal("fixture must be partial")
	}
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.WriteCount() != 0 || s.DB().Articles["from/a.md"].Deleted {
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
	_, err = manager.pendingBundle(context.Background(), GitHubConnection{ID: "handbook", Repository: f.Name, Branch: "main"}, head, tree.SHA, "", SyncJob{Path: "new.md", Revision: 1, Assets: map[string]BlobRef{"img/icon.png": ref}}, Article{Versions: []Revision{{Text: "![icon](img/icon.png)"}}})
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
	move, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 2, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
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
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "imported", DestinationPath: "b.md", ConfirmMove: true})
	if err != ErrInvalid || s.DB().Articles["from/a.md"].Deleted {
		t.Fatal("imported destination accepted", err)
	}
}

func TestImageTransferLimitsDependencies(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	ref := fixtureAsset(t, s, "from", "img/0.png")
	s.Lock()
	db := s.Clone()
	src := db.Sources["from"]
	src.Assets = map[string]BlobRef{}
	var content strings.Builder
	for i := 0; i < 2001; i++ {
		name := fmt.Sprintf("img/%d.png", i)
		src.Assets[name] = ref
		fmt.Fprintf(&content, "![image](%s)\n", name)
	}
	db.Sources["from"] = src
	if err := s.Commit(db); err != nil {
		s.Unlock()
		t.Fatal(err)
	}
	s.Unlock()
	if _, err := s.put("from", "a.md", content.String(), 0); err != nil {
		t.Fatal(err)
	}
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"})
	if err != ErrInvalid {
		t.Fatal("dependency limit not enforced", err)
	}
}
