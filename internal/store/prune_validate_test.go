package store_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func revisionText(n int) string { return "# R" + strconv.Itoa(n) }

func revisionSHA(n int) string { return domain.Digest([]byte(revisionText(n))) }

func prunedArticle(sourceID, p string, numbers ...int) domain.Article {
	a := domain.Article{SourceID: sourceID, Path: p}
	for _, n := range numbers {
		a.Versions = append(a.Versions, domain.Revision{Number: n, Text: revisionText(n), SHA: revisionSHA(n)})
	}
	return a
}

// prunedDB holds one/a.md with revisions 4..6 (1..3 pruned) and two/a.md.
func prunedDB(target ...int) domain.Database {
	return domain.Database{Version: 20,
		Sources: map[string]domain.Source{
			"one": {ID: "one", Title: "One", Kind: "wiki", Visibility: "private"},
			"two": {ID: "two", Title: "Two", Kind: "wiki", Visibility: "private"},
		},
		Articles: map[string]domain.Article{
			"one/a.md": prunedArticle("one", "a.md", 4, 5, 6),
			"one/b.md": prunedArticle("one", "b.md", 4, 5, 6),
			"two/a.md": prunedArticle("two", "a.md", target...),
		},
		Outbox: map[string]domain.SyncJob{},
		Moves:  map[string]domain.Move{},
	}
}

const moveID = "0123456789abcdef0123456789abcdef"

func withMove(db domain.Database, state string, revision int, sha string) domain.Database {
	db.Moves[moveID] = domain.Move{ID: moveID, SourceID: "one", SourcePath: "a.md", SourceRevision: revision,
		DestinationID: "two", DestinationPath: "a.md", SHA: sha, State: state}
	return db
}

func TestValidateTerminalMoveWithPrunedRevisions(t *testing.T) {
	for _, state := range []string{"complete", "cancelled", "source_changed", "destination_changed"} {
		// Both the source revision 1 and the destination's first revision are gone.
		db := withMove(prunedDB(4, 5, 6), state, 1, revisionSHA(1))
		if err := store.Validate(&db); err != nil {
			t.Fatal(state, err)
		}
		// A revision that is still stored must still match.
		db = withMove(prunedDB(4, 5, 6), state, 5, revisionSHA(4))
		if err := store.Validate(&db); err == nil {
			t.Fatal(state, "accepted a stored source revision with another SHA")
		}
		db = withMove(prunedDB(4, 5, 6), state, 7, revisionSHA(7))
		if err := store.Validate(&db); err == nil {
			t.Fatal(state, "accepted a source revision that never existed")
		}
	}
}

func TestValidateActiveMoveNeedsItsRevisions(t *testing.T) {
	// Lookup by number in a gapped history.
	db := withMove(prunedDB(6), "waiting_destination", 6, revisionSHA(6))
	if err := store.Validate(&db); err != nil {
		t.Fatal("gapped active move", err)
	}
	db = withMove(prunedDB(1), "waiting_destination", 1, revisionSHA(1))
	if err := store.Validate(&db); err == nil || !strings.Contains(err.Error(), "move") {
		t.Fatal("active move without its source revision", err)
	}
	db = withMove(prunedDB(4, 5, 6), "waiting_destination", 6, revisionSHA(6))
	if err := store.Validate(&db); err == nil {
		t.Fatal("active move without its destination revision")
	}
}

func withJob(db domain.Database, state string, revision int, sha string, bundle map[string]string) domain.Database {
	id := "one/a.md@" + strconv.Itoa(revision)
	db.Outbox[id] = domain.SyncJob{ID: id, SourceID: "one", Path: "a.md", Revision: revision, SHA: sha, State: state, Bundle: bundle}
	return db
}

func TestValidateTerminalOutboxJobWithPrunedRevisions(t *testing.T) {
	for _, state := range []string{"complete", "superseded"} {
		db := withJob(prunedDB(1), state, 1, revisionSHA(1), map[string]string{"b.md": revisionSHA(1)})
		if err := store.Validate(&db); err != nil {
			t.Fatal(state, err)
		}
		db = withJob(prunedDB(1), state, 5, revisionSHA(4), nil)
		if err := store.Validate(&db); err == nil {
			t.Fatal(state, "accepted a stored revision with another SHA")
		}
		db = withJob(prunedDB(1), state, 7, revisionSHA(7), nil)
		if err := store.Validate(&db); err == nil {
			t.Fatal(state, "accepted a revision that never existed")
		}
	}
}

func TestValidateActiveOutboxJobNeedsItsRevisions(t *testing.T) {
	db := withJob(prunedDB(1), "pending", 5, revisionSHA(5), map[string]string{"b.md": revisionSHA(4)})
	if err := store.Validate(&db); err != nil {
		t.Fatal("gapped active job", err)
	}
	db = withJob(prunedDB(1), "pending", 1, revisionSHA(1), nil)
	if err := store.Validate(&db); err == nil {
		t.Fatal("active job without its revision")
	}
	db = withJob(prunedDB(1), "retry", 5, revisionSHA(5), map[string]string{"b.md": revisionSHA(1)})
	if err := store.Validate(&db); err == nil {
		t.Fatal("active bundle without the first revision of its article")
	}
}

func TestValidateReconcileBaseByNumberInGappedHistory(t *testing.T) {
	db := prunedDB(1)
	target := db.Articles["two/a.md"]
	target.ReconcileBase = &domain.ReconcileBase{SourceID: "one", Path: "a.md", Revision: 5, SHA: revisionSHA(5)}
	db.Articles["two/a.md"] = target
	if err := store.Validate(&db); err != nil {
		t.Fatal("gapped baseline", err)
	}
	target.ReconcileBase = &domain.ReconcileBase{SourceID: "one", Path: "a.md", Revision: 2, SHA: revisionSHA(2)}
	db.Articles["two/a.md"] = target
	if err := store.Validate(&db); err == nil {
		t.Fatal("accepted a pruned baseline")
	}
}

func TestPrunedStoreStaysValidAsProtectionsLift(t *testing.T) {
	db := prunedDB(1)
	db.Articles["one/a.md"] = prunedArticle("one", "a.md", 1, 2, 3, 4, 5, 6)
	db.Articles["one/b.md"] = prunedArticle("one", "b.md", 1, 2, 3, 4, 5)
	db = withJob(db, "pending", 2, revisionSHA(2), map[string]string{"b.md": revisionSHA(1)})
	db = withMove(db, "waiting_destination", 1, revisionSHA(1))
	// a.md keeps 1 (move) and 2 (job); b.md keeps 1 (bundle).
	if removed := domain.PruneAll(&db); removed != 2 {
		t.Fatal("removed", removed)
	}
	if err := store.Validate(&db); err != nil {
		t.Fatal("protected store", err)
	}
	for _, n := range []int{1, 2} {
		if _, ok := domain.RevisionByNumber(db.Articles["one/a.md"], n); !ok {
			t.Fatal("protected revision pruned", n)
		}
	}
	job := db.Outbox["one/a.md@2"]
	job.State = "complete"
	db.Outbox["one/a.md@2"] = job
	move := db.Moves[moveID]
	move.State = "complete"
	db.Moves[moveID] = move
	target := db.Articles["two/a.md"]
	target.Versions = prunedArticle("two", "a.md", 1, 2, 3, 4).Versions
	db.Articles["two/a.md"] = target
	if removed := domain.PruneAll(&db); removed != 4 {
		t.Fatal("removed after protections lifted", removed)
	}
	if err := store.Validate(&db); err != nil {
		t.Fatal("store after protections lifted", err)
	}
}
