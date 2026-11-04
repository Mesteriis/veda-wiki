package importer

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestResolveConflictExpectsTheLatestRevisionNumberOfAGappedHistory(t *testing.T) {
	s := newFixture(t)
	b := testBatch()
	if _, e := s.importBatch(b); e != nil {
		t.Fatal(e)
	}
	if _, e := s.put("handbook", "README.md", "# Local", 1); e != nil {
		t.Fatal(e)
	}
	b = applyAgain(s, b)
	b.Files = map[string]string{"README.md": "# Remote"}
	b.Commit = strings.Repeat("b", 40)
	if result, e := s.importBatch(b); e != nil || result.Conflicts != 1 {
		t.Fatal(result, e)
	}
	storetest.Renumber(t, s.Store, "handbook/README.md", 8)
	if got := s.Conflicts(); len(got) != 1 || got[0].Revision != 9 {
		t.Fatal("conflict lists a position, not the latest revision number", got)
	}
	if e := s.ResolveConflict("handbook", "README.md", 0, 3, "upstream"); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale expected revision accepted", e)
	}
	if e := s.ResolveConflict("handbook", "README.md", 0, 9, "upstream"); e != nil {
		t.Fatal(e)
	}
	a := s.db().Articles["handbook/README.md"]
	if last, _ := domain.LastRevision(a); last.Number != 10 || last.Text != "# Remote" {
		t.Fatal("resolution did not append revision 10", last)
	}
}
