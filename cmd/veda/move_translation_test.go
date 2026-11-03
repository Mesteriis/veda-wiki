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

func programSource(t *testing.T, s *Store, id, visibility string) {
	t.Helper()
	if err := s.addSource(Source{ID: id, Title: id, Kind: "program", Visibility: visibility}); err != nil {
		t.Fatal(err)
	}
}

func TestOldBinaryRejectsTranslatedMoveStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V5_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	programSource(t, s, "from", "private")
	programSource(t, s, "to", "private")
	rev, _ := s.put("from", "docs/a.md", "# Original", 0)
	if _, err := s.translation().Submit(translationRequest{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true}); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(s.Dir(), "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, old, "--data", s.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0")
	var exit *exec.ExitError
	output, err := command.CombinedOutput()
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(output), "unsupported store") {
		t.Fatal("old binary did not reject version 5")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(after) != string(before) {
		t.Fatal("old binary changed the store")
	}
}

func TestMovePreservesTranslationAsPrivateDraft(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "from", "private")
	programSource(t, s, "to", "public")
	rev, err := s.put("from", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.translation().Submit(translationRequest{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil || m.State != "complete" || m.TranslationFingerprint == "" {
		t.Fatal("move", m, err)
	}
	private, err := s.page("to/docs/b.md", false)
	if err != nil || !private.Translated || private.Text != "# Перевод" {
		t.Fatal("translation not retained", err)
	}
	public, err := s.page("to/docs/b.md", true)
	if err != nil || public.Translated || public.Text != "# Original" {
		t.Fatal("translation became public", err)
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.DB().Version != 5 {
		t.Fatal("store version or restart", err)
	}
	if reopened.moves()[0].TranslationFingerprint != m.TranslationFingerprint {
		t.Fatal("move fingerprint lost")
	}
	if len(reopened.auth().AccessScope(key, time.Now())) != 1 {
		t.Fatal("scope lost after restart")
	}
	if err := reopened.auth().RevokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	reopened, err = openStore(s.Dir())
	if err != nil || reopened.DB().Version != 5 || len(reopened.auth().AccessScope(key, time.Now())) != 0 {
		t.Fatal("revocation did not survive restart", err)
	}
}

func TestCopyRetainsTranslationWithoutPublishingIt(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "from", "private")
	programSource(t, s, "to", "public")
	rev, _ := s.put("from", "docs/a.md", "# Original", 0)
	if _, err := s.translation().Submit(translationRequest{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.copyArticle(copyRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.page("to/docs/b.md", false)
	if err != nil || !private.Translated {
		t.Fatal("private translation lost", err)
	}
	public, err := s.page("to/docs/b.md", true)
	if err != nil || public.Translated {
		t.Fatal("translation published without review", err)
	}
	if _, err := s.article("from", "docs/a.md", false); err != nil {
		t.Fatal("copy removed source", err)
	}
}

func TestTranslatedMoveKeepsSourceWhenTranslationChanges(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "from", "private")
	rev, _ := s.put("from", "docs/a.md", "# Original", 0)
	req := translationRequest{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}
	if _, err := s.translation().Submit(req); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Fixture"})
	manager := connectFixture(t, s, f, true)
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "docs/moved.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	req.Text = "# Исправленный перевод"
	if _, err = s.translation().Submit(req); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if s.moves()[0].State != "source_changed" || s.DB().Articles["from/docs/a.md"].Deleted {
		t.Fatal("changed translation lost")
	}
}

func TestTranslatedMoveKeepsSourceWhenDestinationTranslationChanges(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "from", "private")
	rev, _ := s.put("from", "docs/a.md", "# Original", 0)
	if _, err := s.translation().Submit(translationRequest{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Fixture"})
	connectFixture(t, s, f, true)
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "docs/moved.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.translation().Submit(translationRequest{SourceID: "handbook", Path: "docs/moved.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Новый перевод", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.moves()[0].State != "destination_changed" || s.DB().Articles["from/docs/a.md"].Deleted {
		t.Fatal("source lost after destination translation changed")
	}
}

func TestConnectedSourceMoveRetainsTranslation(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/a.md": "# Original"})
	manager := connectFixture(t, s, f, true)
	article, err := s.article("handbook", "docs/a.md", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.translation().Submit(translationRequest{SourceID: "handbook", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: article.Versions[0].SHA, Text: "# Перевод", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	programSource(t, s, "to", "private")
	_, err = s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" {
		t.Fatal("move did not complete", s.moves())
	}
	page, err := s.page("to/docs/b.md", false)
	if err != nil || !page.Translated || page.Text != "# Перевод" {
		t.Fatal("translated copy missing", err)
	}
	if _, exists := f.Contents()["docs/a.md"]; exists {
		t.Fatal("source remained remotely")
	}
}
