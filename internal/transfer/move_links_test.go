package transfer

import (
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/render"
)

func TestMovePreservesMatchingRelativeLink(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	rev, err := s.put("from", "guide.md", "# Shared guide", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("to", "guide.md", "# Shared guide", 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("from", "section/a.md", "# Article\n\n[Read](../guide.md#start)", 0); err != nil {
		t.Fatal(err)
	}
	m, err := s.Move(MoveRequest{SourceID: "from", SourcePath: "section/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "chapter/b.md", ConfirmMove: true})
	if err != nil || m.State != "complete" || m.Links["guide.md"] != rev.SHA {
		t.Fatal("linked move", m, err)
	}
	if _, err := s.auth().CreateAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Version != 8 || reopened.Moves()[0].Links["guide.md"] != rev.SHA {
		t.Fatal("v8 restart", err)
	}
	page, err := reopened.page("to/chapter/b.md", false)
	if err != nil {
		t.Fatal(err)
	}
	html, _, err := render.Page(page)
	if err != nil || !strings.Contains(string(html), "to%2Fguide.md#start") {
		t.Fatal("link was not preserved", err)
	}
}
