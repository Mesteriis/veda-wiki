package agentctx

import (
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestProposalBaseReportsRevisionLocksAndStatus(t *testing.T) {
	st := storetest.New(t)
	connected(t, st, "handbook")
	for _, name := range []string{"a.md", "b.md", "linked.md"} {
		article(t, st, "handbook", name, "# "+name)
	}
	storetest.Mutate(t, st, func(db *domain.Database) {
		a := db.Articles["handbook/a.md"]
		a.Conflicts = []domain.ImportConflict{{}}
		db.Articles["handbook/a.md"] = a
		db.Outbox["prepared"] = domain.SyncJob{SourceID: "handbook", Path: "a.md", State: "retry", PreparedCommit: "p"}
		db.Outbox["bundle"] = domain.SyncJob{SourceID: "handbook", Path: "b.md", State: "pending", Bundle: map[string]string{"linked.md": strings.Repeat("a", 64)}}
		db.Moves["m"] = domain.Move{ID: "m", SourceID: "handbook", SourcePath: "gone.md", DestinationID: "handbook", DestinationPath: "b.md", State: "deleting_source"}
	})
	a, err := ProposalBase(st, nil, "handbook", "a.md")
	if err != nil || a["revision"] != 1 || a["sha256"] != domain.Digest([]byte("# a.md")) || a["unresolved_conflict"] != true || a["conflict_resolution_blocked_by_write"] != true || a["write_blocked_by_move"] != false || a["write_blocked_by_bundle"] != false {
		t.Fatal("a.md base", a, err)
	}
	status, ok := a["source_status"].(domain.SourceStatus)
	if !ok || status.UnconfirmedWrites == nil || *status.UnconfirmedWrites != 2 || *status.PreparedWrites != 1 || status.UnresolvedConflicts != 1 {
		t.Fatal("status", a["source_status"])
	}
	b, err := ProposalBase(st, nil, "handbook", "b.md")
	if err != nil || b["unresolved_conflict"] != false || b["write_blocked_by_move"] != true || b["write_blocked_by_bundle"] != true {
		t.Fatal("b.md base", b, err)
	}
	linked, _ := ProposalBase(st, nil, "handbook", "linked.md")
	if linked["write_blocked_by_bundle"] != true || linked["conflict_resolution_blocked_by_write"] != true {
		t.Fatal("bundle member not locked", linked)
	}
}

func TestProposalBaseErrors(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "alpha", "private")
	article(t, st, "alpha", "gone.md", "# Gone")
	storetest.Mutate(t, st, func(db *domain.Database) {
		a := db.Articles["alpha/gone.md"]
		a.Deleted = true
		db.Articles["alpha/gone.md"] = a
	})
	for path, want := range map[string]error{"missing.md": domain.ErrNotFound, "gone.md": domain.ErrNotFound, "../x.md": domain.ErrInvalid} {
		if _, err := ProposalBase(st, nil, "alpha", path); err != want {
			t.Fatal(path, err)
		}
	}
	if _, err := ProposalBase(st, nil, "nope", "a.md"); err != domain.ErrNotFound {
		t.Fatal(err)
	}
}

func TestNewProposalBaseChecksSourceAndVisiblePath(t *testing.T) {
	st := storetest.New(t)
	storetest.Source(t, st, "alpha", "private")
	article(t, st, "alpha", "exists.md", "# Exists")
	article(t, st, "alpha", "gone.md", "# Gone")
	storetest.Mutate(t, st, func(db *domain.Database) {
		a := db.Articles["alpha/gone.md"]
		a.Deleted = true
		db.Articles["alpha/gone.md"] = a
	})
	if _, err := NewProposalBase(st, nil, "alpha", "exists.md"); err != domain.ErrConflict {
		t.Fatal(err)
	}
	if _, err := NewProposalBase(st, nil, "nope", "new.md"); err != domain.ErrNotFound {
		t.Fatal(err)
	}
	if _, err := NewProposalBase(st, nil, "alpha", "../new.md"); err != domain.ErrInvalid {
		t.Fatal(err)
	}
	for _, path := range []string{"new.md", "gone.md"} {
		base, err := NewProposalBase(st, nil, "alpha", path)
		if err != nil || len(base) != 1 || base["source_status"] == nil {
			t.Fatal(path, base, err)
		}
	}
}
