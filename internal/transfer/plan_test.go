package transfer

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestTransferPlanCyclesAndDestinationStates(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	s.put("from", "a.md", "[B](b.md) [C](c.md)", 0)
	s.put("from", "b.md", "[A](a.md)", 0)
	s.put("from", "c.md", "# C", 0)
	s.put("to", "b.md", "[A](a.md)", 0)
	s.put("to", "c.md", "# different", 0)
	before := s.state()
	p, err := s.PlanTransfer(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md"})
	if err != nil || len(p.Articles) != 3 {
		t.Fatal(p, err)
	}
	if p.Articles[0].Destination != "missing" || p.Articles[1].Destination != "matching" || p.Articles[2].Destination != "conflict" {
		t.Fatal(p)
	}
	after := s.state()
	if before != after {
		t.Fatal("plan changed state")
	}
}
func TestTransferPlanRejectsMissingAndAmbiguousPaths(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	req := CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "renamed.md"}
	if _, err := s.PlanTransfer(req); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	s.put("from", "b.md", "[A](a.md)", 0)
	if _, err := s.PlanTransfer(req); err != domain.ErrConflict {
		t.Fatal(err)
	}
	req.ExpectedRevision = 2
	if _, err := s.PlanTransfer(req); err != domain.ErrConflict {
		t.Fatal(err)
	}
}

func TestTransferPlanReportsStatusOfBothSourcesFromOneSnapshot(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "public")
	s.put("from", "a.md", "# A", 0)
	statuses := map[string]string{}
	s.Status = func(db *domain.Database, sourceID string, src domain.Source) any {
		if db.Sources[sourceID].ID != src.ID {
			t.Fatal("status source does not come from the plan snapshot", sourceID)
		}
		statuses[sourceID] = src.Visibility
		return src.Visibility
	}
	plan, err := s.PlanTransfer(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md"})
	if err != nil || plan.SourceStatus != "private" || plan.DestinationStatus != "public" || len(statuses) != 2 {
		t.Fatal("source statuses missing from plan", plan, err)
	}
	clear(statuses)
	if _, err = s.PlanTransfer(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 2, DestinationID: "to", DestinationPath: "a.md"}); err != domain.ErrConflict || len(statuses) != 0 {
		t.Fatal("rejected plan reported statuses", err, statuses)
	}
}
