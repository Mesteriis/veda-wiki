package transfer

import (
	"errors"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestMoveExpectsTheLatestRevisionNumberOfAGappedHistory(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	for i, text := range []string{"# One", "# Two", "# Three"} {
		if _, e := s.put("from", "a.md", text, i); e != nil {
			t.Fatal(e)
		}
	}
	storetest.Renumber(t, s.Store, "from/a.md", 7)
	req := MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 3, DestinationID: "to", DestinationPath: "a.md", ConfirmMove: true}
	if _, e := s.Move(req); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale expected revision accepted", e)
	}
	req.ExpectedRevision = 9
	m, e := s.Move(req)
	if e != nil || m.State != "complete" || m.SourceRevision != 9 {
		t.Fatal(m, e)
	}
	target := s.db().Articles["to/a.md"]
	if target.Versions[0].Text != "# Three" || target.CopiedFrom == nil || target.CopiedFrom.Revision != 9 {
		t.Fatal("moved the wrong revision", target)
	}
}
