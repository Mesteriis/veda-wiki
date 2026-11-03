package agentctx

import (
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestContextScopesToSourceAndReportsStatus(t *testing.T) {
	st := storetest.New(t)
	for _, id := range []string{"alpha", "beta"} {
		storetest.Source(t, st, id, "private")
		article(t, st, id, "AGENTS.md", "# "+id+" root")
		article(t, st, id, "infra/AGENTS.md", "# "+id+" infra")
	}
	storetest.Mutate(t, st, func(db *domain.Database) {
		src := db.Sources["alpha"]
		src.ImportPartial, src.ImportDirty = true, true
		db.Sources["alpha"] = src
		a := db.Articles["alpha/infra/AGENTS.md"]
		a.Conflicts = []domain.ImportConflict{{}}
		db.Articles["alpha/infra/AGENTS.md"] = a
	})
	obs := &importer.Observations{}
	obs.MarkObserved("alpha", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	got, err := Context(st, obs, "alpha", "infra/vps/guide.md", false)
	if err != nil || len(got.Instructions) != 2 || got.Instructions[0].Path != "AGENTS.md" || got.Instructions[1].Path != "infra/AGENTS.md" || got.Instructions[1].SourceID != "alpha" || !strings.Contains(got.Instructions[1].Text, "alpha infra") {
		t.Fatal("instructions", got, err)
	}
	if len(got.Missing) != 1 || got.Missing[0] != "infra/vps/AGENTS.md" {
		t.Fatal("missing", got.Missing)
	}
	s := got.SourceStatus
	if !s.ImportPartial || !s.ImportDirty || s.UnresolvedConflicts != 1 || s.LastImportObservedAt == nil || *s.LastImportObservedAt != "2026-01-01T00:00:00Z" {
		t.Fatal("status", s)
	}
	if got.Instructions[1].Number != 1 {
		t.Fatal("owner context hides the revision number")
	}
	beta, err := Context(st, obs, "beta", "guide.md", false)
	if err != nil || beta.SourceStatus.LastImportObservedAt != nil || beta.SourceStatus.UnresolvedConflicts != 0 || len(beta.Instructions) != 1 {
		t.Fatal("beta leaked alpha status", beta, err)
	}
}

func TestContextRejectsUnknownSourceAndBadPath(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "alpha", "private")
	storetest.Source(t, st, "open", "public")
	if _, err := Context(st, nil, "missing", "guide.md", false); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	if _, err := Context(st, nil, "alpha", "../guide.md", false); err != domain.ErrInvalid {
		t.Fatal(err)
	}
	if _, err := Context(st, nil, "alpha", "guide.md", true); err != domain.ErrNotFound {
		t.Fatal("public reader saw a private source", err)
	}
	if _, err := Context(st, nil, "open", "guide.md", true); err != nil {
		t.Fatal(err)
	}
}

func TestContextRefusesOversizedInstructionSet(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "alpha", "private")
	text := strings.Repeat("x", 220000)
	parts := []string{}
	for i := 0; i < 7; i++ {
		name := "AGENTS.md"
		if i > 0 {
			name = strings.Join(parts, "/") + "/AGENTS.md"
		}
		article(t, st, "alpha", name, text)
		parts = append(parts, "d")
	}
	if _, err := Context(st, nil, "alpha", strings.Join(parts, "/")+"/guide.md", false); err != ErrContextLimit {
		t.Fatal("oversized context was not rejected", err)
	}
}

func TestPublicContextUsesPublishedRevision(t *testing.T) {
	st := storetest.New(t)
	connected(t, st, "handbook")
	storetest.Mutate(t, st, func(db *domain.Database) {
		published := domain.Revision{Number: 1, Text: "# Published instructions"}
		private := domain.Revision{Number: 2, Text: "# Private local instructions"}
		db.Articles["handbook/AGENTS.md"] = domain.Article{SourceID: "handbook", Path: "AGENTS.md", Versions: []domain.Revision{published, private}, Published: &published, Conflicts: []domain.ImportConflict{{}}}
		db.Articles["handbook/infra/AGENTS.md"] = domain.Article{SourceID: "handbook", Path: "infra/AGENTS.md", Versions: []domain.Revision{private}}
	})
	obs := &importer.Observations{}
	obs.MarkObserved("handbook", time.Now())
	got, err := Context(st, obs, "handbook", "infra/guide.md", true)
	if err != nil || len(got.Instructions) != 1 || got.Instructions[0].Text != "# Published instructions" || got.Instructions[0].Number != 0 {
		t.Fatal("public context revealed a private revision", got, err)
	}
	if len(got.Missing) != 1 || got.Missing[0] != "infra/AGENTS.md" {
		t.Fatal("unpublished instruction was not reported missing", got.Missing)
	}
	s := got.SourceStatus
	if s.LastImportObservedAt != nil || s.UnresolvedConflicts != 0 || s.SyncState != "" || s.SyncError != "" || s.SyncVerification != "" || s.SyncIssue != "" || s.UnconfirmedWrites != nil || s.PreparedWrites != nil {
		t.Fatal("public context revealed private status", s)
	}
	owner, err := Context(st, obs, "handbook", "guide.md", false)
	if err != nil || owner.Instructions[0].Text != "# Private local instructions" || owner.SourceStatus.UnresolvedConflicts != 1 || owner.SourceStatus.SyncVerification != "verified" {
		t.Fatal("owner context", owner, err)
	}
}

func TestContextReportsUnconfirmedWritesFromTheSnapshot(t *testing.T) {
	st := storetest.New(t)
	connected(t, st, "handbook")
	storetest.Source(t, st, "other", "private")
	storetest.Mutate(t, st, func(db *domain.Database) {
		db.Outbox["a"] = domain.SyncJob{SourceID: "handbook", State: "pending"}
		db.Outbox["b"] = domain.SyncJob{SourceID: "handbook", State: "retry", PreparedCommit: "p"}
		db.Outbox["c"] = domain.SyncJob{SourceID: "handbook", State: "complete"}
	})
	got, err := Context(st, nil, "handbook", "guide.md", false)
	if err != nil || got.SourceStatus.UnconfirmedWrites == nil || *got.SourceStatus.UnconfirmedWrites != 2 || *got.SourceStatus.PreparedWrites != 1 {
		t.Fatal("outbox counts", got.SourceStatus, err)
	}
	pub, err := Context(st, nil, "handbook", "guide.md", true)
	if err != nil || pub.SourceStatus.UnconfirmedWrites != nil || pub.SourceStatus.PreparedWrites != nil {
		t.Fatal("public context exposed the write queue", pub.SourceStatus, err)
	}
	local, err := Context(st, nil, "other", "guide.md", false)
	if err != nil || local.SourceStatus.UnconfirmedWrites != nil || local.SourceStatus.PreparedWrites != nil {
		t.Fatal("local source reported a GitHub outbox count", err)
	}
}
