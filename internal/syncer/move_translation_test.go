package syncer

import (
	"context"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
	"github.com/Mesteriis/veda-wiki/internal/translation"
)

func TestTranslatedMoveKeepsSourceWhenTranslationChanges(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "from", "private")
	rev, _ := s.put("from", "docs/a.md", "# Original", 0)
	req := translation.Request{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}
	if _, err := s.translation().Submit(req); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Fixture"})
	manager := connectFixture(t, s, f, true)
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "docs/moved.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	req.Text = "# Исправленный перевод"
	if _, err = s.translation().Submit(req); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if s.moves()[0].State != "source_changed" || s.db().Articles["from/docs/a.md"].Deleted {
		t.Fatal("changed translation lost")
	}
}

func TestTranslatedMoveKeepsSourceWhenDestinationTranslationChanges(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "from", "private")
	rev, _ := s.put("from", "docs/a.md", "# Original", 0)
	if _, err := s.translation().Submit(translation.Request{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Fixture"})
	connectFixture(t, s, f, true)
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "docs/moved.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.translation().Submit(translation.Request{SourceID: "handbook", Path: "docs/moved.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Новый перевод", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.moves()[0].State != "destination_changed" || s.db().Articles["from/docs/a.md"].Deleted {
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
	_, err = s.translation().Submit(translation.Request{SourceID: "handbook", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: article.Versions[0].SHA, Text: "# Перевод", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	programSource(t, s, "to", "private")
	_, err = s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true})
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
