package httpapi

import (
	"context"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
	"github.com/Mesteriis/veda-wiki/internal/translation"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func fixtureAsset(t *testing.T, s *Store, sourceID, assetPath string) domain.BlobRef {
	t.Helper()
	image := smallPNG(t)
	ref, err := domain.ImageRef(image)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(s.Dir(), "blobs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.Dir(), "blobs", ref.SHA), image, 0600); err != nil {
		t.Fatal(err)
	}
	s.mutate(t, func(db *domain.Database) {
		src := db.Sources[sourceID]
		src.Assets = map[string]domain.BlobRef{assetPath: ref}
		db.Sources[sourceID] = src
	})
	return ref
}

func TestMoveCopiesRelativeImageToLocalDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	ref := fixtureAsset(t, s, "from", "guide/img/icon.png")
	if _, err := s.put("from", "guide/a.md", "# Guide\n\n![icon](img/icon.png)", 0); err != nil {
		t.Fatal(err)
	}
	m, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "guide/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "nested/b.md", ConfirmMove: true})
	if err != nil || m.State != "complete" || m.Assets["nested/img/icon.png"] != ref {
		t.Fatal("move failed", m, err)
	}
	if s.state(t).Sources["to"].Assets["nested/img/icon.png"] != ref {
		t.Fatal("target image missing")
	}
	if _, err := s.auth().CreateAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.state(t).Version != domain.RevisionsFormat || reopened.moves()[0].Assets["nested/img/icon.png"] != ref {
		t.Fatal("move did not survive restart", err)
	}
	w := Wiki{Store: reopened, Token: "test-owner"}
	r := request(w, "GET", "/media/to/nested/img/icon.png", "", true)
	if r.Code != 200 {
		t.Fatal("image unavailable", r.Code)
	}
}

func TestPublicImageCopyRequiresPublicationConfirmation(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "public")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	in := transfer.CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md"}
	if _, err := s.copyArticle(in); err != domain.ErrInvalid {
		t.Fatal("private image published without confirmation", err)
	}
	if len(s.state(t).Sources["to"].Assets) != 0 {
		t.Fatal("refused image published")
	}
	in.ConfirmPublication = true
	if _, err := s.copyArticle(in); err != nil {
		t.Fatal(err)
	}
	if s.state(t).Sources["to"].Assets["img/icon.png"] != ref {
		t.Fatal("confirmed image missing")
	}
	if r := request(Wiki{Store: s}, "GET", "/media/to/img/icon.png", "", false); r.Code != 200 {
		t.Fatal("confirmed public image unavailable", r.Code)
	}
}

func TestImageTransferToConnectedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	ref := fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "![icon](img/icon.png)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	manager := connectFixture(t, s, f, true)
	m, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil || m.State != "waiting_destination" || s.state(t).Version != domain.RevisionsFormat {
		t.Fatal("move not queued", err, m)
	}
	if r := request(Wiki{Store: s}, "GET", "/media/handbook/img/icon.png", "", false); r.Code != 404 {
		t.Fatal("pending image exposed", r.Code)
	}
	manager.Once(context.Background())
	if s.state(t).Articles["from/a.md"].Deleted || f.WriteCount() != 1 || f.Contents()["b.md"] == "" || domain.Digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA {
		t.Fatal("bundle was not committed atomically")
	}
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || !s.state(t).Articles["from/a.md"].Deleted || s.state(t).Sources["handbook"].Assets["img/icon.png"] != ref {
		t.Fatal("observed bundle did not finish move", s.moves())
	}
	if _, err := s.auth().CreateAccessKey("agent", []string{"handbook"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.state(t).Version != domain.RevisionsFormat {
		t.Fatal("queued assets did not persist", err)
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
	_, err = s.translation().Submit(translation.Request{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод\n\n![значок](img/icon.png)"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/sub/b.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.auth().CreateAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.state(t).Version != domain.RevisionsFormat {
		t.Fatal("v6 restart failed", err)
	}
	page, err := reopened.page("to/docs/sub/b.md", false)
	if err != nil || !page.Translated || reopened.state(t).Sources["to"].Assets["docs/sub/img/icon.png"] != ref {
		t.Fatal("translation or image lost", err)
	}
}
