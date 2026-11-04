package translation

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestWorkerCandidateExpectsTheLatestRevisionNumberOfAGappedHistory(t *testing.T) {
	s := newFixture(t)
	programSource(t, s, "code", "private")
	for i, text := range []string{"# One", "# Two", "# Three"} {
		if _, e := s.put("code", "docs/a.md", text, i); e != nil {
			t.Fatal(e)
		}
	}
	storetest.Renumber(t, s.Store, "code/docs/a.md", 7)
	w := &Worker{store: s.Store}
	sha := domain.Digest([]byte("# Three"))
	if _, ok := w.original(Candidate{SourceID: "code", Path: "docs/a.md", Revision: 3, OriginalSHA: sha}); ok {
		t.Fatal("stale candidate revision accepted")
	}
	rev, ok := w.original(Candidate{SourceID: "code", Path: "docs/a.md", Revision: 9, OriginalSHA: sha})
	if !ok || rev.Number != 9 || rev.Text != "# Three" {
		t.Fatal("latest candidate revision refused", rev, ok)
	}
}
