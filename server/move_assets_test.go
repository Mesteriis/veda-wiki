package main

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
)

func fixtureAsset(t *testing.T, s *Store, sourceID, assetPath string) BlobRef {
	t.Helper()
	image := smallPNG(t)
	ref, err := imageRef(image)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(s.dir, "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.dir, "blobs", ref.SHA), image, 0600); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.clone()
	src := db.Sources[sourceID]
	src.Assets = map[string]BlobRef{assetPath: ref}
	db.Sources[sourceID] = src
	if err = s.commit(db); err != nil {
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
	state := filepath.Join(s.dir, "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
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
	if s.db.Sources["to"].Assets["nested/img/icon.png"] != ref {
		t.Fatal("target image missing")
	}
	if _, err := s.createAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Version != 6 || reopened.moves()[0].Assets["nested/img/icon.png"] != ref {
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
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Version != 6 {
		t.Fatal("v6 restart failed", err)
	}
	page, err := reopened.page("to/docs/sub/b.md", false)
	if err != nil || !page.Translated || reopened.db.Sources["to"].Assets["docs/sub/img/icon.png"] != ref {
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
	if s.db.Sources["to"].Assets["img/icon.png"] != ref || s.db.Articles["from/a.md"].Deleted {
		t.Fatal("copy lost data")
	}
	s.put("from", "b.md", "![icon](img/icon.png)", 0)
	db := s.clone()
	dst := db.Sources["to"]
	dst.Assets = map[string]BlobRef{"img/icon.png": {SHA: strings.Repeat("a", 64), MIME: "image/png"}}
	db.Sources["to"] = dst
	s.mu.Lock()
	s.db = db
	s.mu.Unlock()
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "b.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"})
	if err != ErrConflict {
		t.Fatal("collision accepted", err)
	}
	if _, exists := s.db.Articles["to/b.md"]; exists {
		t.Fatal("failed copy committed")
	}
}

func TestMoveImageWaitsForVerifiedBlobBeforeRemoteDeletion(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "![icon](img/icon.png)", "img/icon.png": string(smallPNG(t))})
	manager := connectFixture(t, s, f, true)
	source(t, s, "to", "private")
	m, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/moved.md", ConfirmMove: true})
	if err != nil || m.State != "waiting_source_delete" {
		t.Fatal("move", m, err)
	}
	ref := m.Assets["nested/img/icon.png"]
	if err = os.WriteFile(filepath.Join(s.dir, "blobs", ref.SHA), []byte("corrupt synthetic image"), 0600); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.writeCount() != 0 || f.contents()["guide.md"] == "" {
		t.Fatal("source deleted despite corrupt image")
	}
}

func TestMoveImageFromConnectedSourceToLocal(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "![icon](img/icon.png)", "img/icon.png": string(smallPNG(t))})
	manager := connectFixture(t, s, f, true)
	source(t, s, "to", "private")
	m, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || f.writeCount() != 1 {
		t.Fatal("remote move did not complete", s.moves())
	}
	if _, exists := f.contents()["guide.md"]; exists {
		t.Fatal("remote source still exists")
	}
	if s.db.Sources["to"].Assets["nested/img/icon.png"] != m.Assets["nested/img/icon.png"] {
		t.Fatal("target image missing")
	}
}

func TestMoveImageDetectsChangedDestinationBeforeDeletion(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "![icon](img/icon.png)", "img/icon.png": string(smallPNG(t))})
	manager := connectFixture(t, s, f, true)
	source(t, s, "to", "private")
	_, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	db := s.clone()
	dst := db.Sources["to"]
	dst.Assets = map[string]BlobRef{}
	db.Sources["to"] = dst
	reconcileMoves(&db)
	if err = s.commit(db); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()
	manager.Once(context.Background())
	if s.moves()[0].State != "destination_changed" || f.writeCount() != 0 || f.contents()["guide.md"] == "" {
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
	if len(s.db.Sources["to"].Assets) != 0 {
		t.Fatal("refused image published")
	}
	in.ConfirmPublication = true
	if _, err := s.copyArticle(in); err != nil {
		t.Fatal(err)
	}
	if s.db.Sources["to"].Assets["img/icon.png"] != ref {
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

func TestImageTransferRejectsConnectedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != ErrInvalid || s.db.Articles["from/a.md"].Deleted || f.writeCount() != 0 {
		t.Fatal("connected destination lost image", err)
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
	if err != ErrInvalid || s.db.Articles["from/a.md"].Deleted {
		t.Fatal("imported destination accepted", err)
	}
}

func TestImageTransferLimitsDependencies(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	ref := fixtureAsset(t, s, "from", "img/0.png")
	s.mu.Lock()
	db := s.clone()
	src := db.Sources["from"]
	src.Assets = map[string]BlobRef{}
	var content strings.Builder
	for i := 0; i < 2001; i++ {
		name := fmt.Sprintf("img/%d.png", i)
		src.Assets[name] = ref
		fmt.Fprintf(&content, "![image](%s)\n", name)
	}
	db.Sources["from"] = src
	if err := s.commit(db); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()
	if _, err := s.put("from", "a.md", content.String(), 0); err != nil {
		t.Fatal(err)
	}
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"})
	if err != ErrInvalid {
		t.Fatal("dependency limit not enforced", err)
	}
}
