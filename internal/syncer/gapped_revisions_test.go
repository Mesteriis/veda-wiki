package syncer

import (
	"context"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestPublishJobExpectsTheLatestRevisionNumberOfAGappedHistory(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	if _, e := s.put("handbook", "README.md", "# Private draft", 1); e != nil {
		t.Fatal(e)
	}
	f.Modify(func() { f.Private = false })
	m.Once(context.Background())
	storetest.Renumber(t, s.st, "handbook/README.md", 8)
	jobs := s.jobs()
	if len(jobs) != 1 || jobs[0].Revision != 9 || jobs[0].LastError != "publication_confirmation_required" {
		t.Fatal("unexpected queue", jobs)
	}
	if e := s.publishJob(jobs[0].ID, 3, true); e != domain.ErrConflict {
		t.Fatal("stale expected revision accepted", e)
	}
	if e := s.publishJob(jobs[0].ID, 9, true); e != nil {
		t.Fatal(e)
	}
	a := s.db().Articles["handbook/README.md"]
	if a.Published == nil || a.Published.Text != "# Private draft" {
		t.Fatal("published the wrong revision", a.Published)
	}
}
