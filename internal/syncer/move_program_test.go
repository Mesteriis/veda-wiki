package syncer

import (
	"context"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

func TestConnectedSourceMoveToProgramSourceCompletes(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/a.md": "# Original"})
	manager := connectFixture(t, s, f, true)
	programSource(t, s, "to", "private")
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" {
		t.Fatal("move did not complete", s.moves())
	}
	page, err := s.page("to/docs/b.md", false)
	if err != nil || page.Text != "# Original" {
		t.Fatal("moved copy missing", err)
	}
	if _, exists := f.Contents()["docs/a.md"]; exists {
		t.Fatal("source remained remotely")
	}
}
