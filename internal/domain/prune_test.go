package domain

import (
	"reflect"
	"strconv"
	"testing"
)

func revisions(numbers ...int) []Revision {
	out := make([]Revision, 0, len(numbers))
	for _, n := range numbers {
		out = append(out, Revision{Number: n, Text: "# " + strconv.Itoa(n), SHA: "sha" + strconv.Itoa(n)})
	}
	return out
}

func numbers(a Article) []int {
	out := []int{}
	for _, r := range a.Versions {
		out = append(out, r.Number)
	}
	return out
}

func pruneDB(versions ...int) Database {
	return Database{
		Sources:  map[string]Source{"one": {ID: "one", Kind: "wiki"}, "two": {ID: "two", Kind: "wiki"}},
		Articles: map[string]Article{"one/a.md": {SourceID: "one", Path: "a.md", Versions: revisions(versions...)}},
		Outbox:   map[string]SyncJob{},
		Moves:    map[string]Move{},
	}
}

func expectRevisions(t *testing.T, db Database, key string, want ...int) {
	t.Helper()
	if got := numbers(db.Articles[key]); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s revisions = %v, want %v", key, got, want)
	}
}

func TestPruneKeepsTheLastThreeRevisions(t *testing.T) {
	db := pruneDB(1, 2, 3, 4, 5)
	shared := db.Articles["one/a.md"].Versions
	if removed := PruneArticle(&db, "one/a.md"); removed != 2 {
		t.Fatal("removed", removed)
	}
	expectRevisions(t, db, "one/a.md", 3, 4, 5)
	if shared[0].Number != 1 || len(shared) != 5 {
		t.Fatal("pruning changed the previous revision slice in place")
	}
	if PruneArticle(&db, "one/a.md") != 0 || PruneArticle(&db, "one/missing.md") != 0 {
		t.Fatal("second prune or a missing article removed revisions")
	}
}

func TestPruneLeavesShortHistories(t *testing.T) {
	db := pruneDB(1, 2)
	if removed := PruneArticle(&db, "one/a.md"); removed != 0 {
		t.Fatal("removed", removed)
	}
	expectRevisions(t, db, "one/a.md", 1, 2)
}

func TestPruneGappedHistoryByNumber(t *testing.T) {
	db := pruneDB(7, 8, 9, 10)
	PruneArticle(&db, "one/a.md")
	expectRevisions(t, db, "one/a.md", 8, 9, 10)
}

func TestActiveOutboxJobProtectsItsRevisionUntilTerminal(t *testing.T) {
	for _, state := range []string{"pending", "prepared", "retry", "blocked", "conflict"} {
		db := pruneDB(1, 2, 3, 4, 5, 6)
		db.Outbox["one/a.md@1"] = SyncJob{ID: "one/a.md@1", SourceID: "one", Path: "a.md", Revision: 1, SHA: "sha1", State: state}
		if got := ProtectedRevisions(db, "one/a.md"); !reflect.DeepEqual(got, map[int]bool{1: true}) {
			t.Fatal(state, "protected", got)
		}
		PruneArticle(&db, "one/a.md")
		expectRevisions(t, db, "one/a.md", 1, 4, 5, 6)
	}
	for _, state := range []string{"complete", "superseded"} {
		db := pruneDB(1, 2, 3, 4, 5, 6)
		db.Outbox["one/a.md@1"] = SyncJob{ID: "one/a.md@1", SourceID: "one", Path: "a.md", Revision: 1, SHA: "sha1", State: state}
		if !JobTerminal(state) {
			t.Fatal("state not terminal", state)
		}
		PruneArticle(&db, "one/a.md")
		expectRevisions(t, db, "one/a.md", 4, 5, 6)
	}
}

func TestActiveBundleProtectsRevisionOneOfItsArticles(t *testing.T) {
	db := pruneDB(1, 2, 3, 4, 5, 6)
	db.Articles["one/b.md"] = Article{SourceID: "one", Path: "b.md", Versions: revisions(1, 2, 3, 4, 5)}
	db.Outbox["one/a.md@6"] = SyncJob{ID: "one/a.md@6", SourceID: "one", Path: "a.md", Revision: 6, SHA: "sha6", State: "pending", Bundle: map[string]string{"b.md": "sha1"}}
	PruneAll(&db)
	expectRevisions(t, db, "one/b.md", 1, 3, 4, 5)
	job := db.Outbox["one/a.md@6"]
	job.State = "complete"
	db.Outbox["one/a.md@6"] = job
	PruneAll(&db)
	expectRevisions(t, db, "one/b.md", 3, 4, 5)
}

func TestActiveMoveProtectsItsSourceRevisionUntilTerminal(t *testing.T) {
	db := pruneDB(1, 2, 3, 4, 5, 6)
	db.Articles["two/a.md"] = Article{SourceID: "two", Path: "a.md", Versions: revisions(1)}
	move := Move{ID: "m", SourceID: "one", SourcePath: "a.md", SourceRevision: 1, DestinationID: "two", DestinationPath: "a.md", SHA: "sha1"}
	for _, state := range []string{"waiting_destination", "waiting_source_delete", "deleting_source"} {
		move.State = state
		db.Moves["m"] = move
		if got := ProtectedRevisions(db, "one/a.md"); !reflect.DeepEqual(got, map[int]bool{1: true}) {
			t.Fatal(state, "protected", got)
		}
		if got := ProtectedRevisions(db, "two/a.md"); !reflect.DeepEqual(got, map[int]bool{1: true}) {
			t.Fatal(state, "target protected", got)
		}
	}
	PruneArticle(&db, "one/a.md")
	expectRevisions(t, db, "one/a.md", 1, 4, 5, 6)
	for _, state := range []string{"complete", "cancelled", "source_changed", "destination_changed"} {
		if !MoveTerminal(state) {
			t.Fatal("state not terminal", state)
		}
	}
	move.State = "complete"
	db.Moves["m"] = move
	PruneArticle(&db, "one/a.md")
	expectRevisions(t, db, "one/a.md", 4, 5, 6)
}

func TestReconcileBaseProtectsTheOriginRevision(t *testing.T) {
	db := pruneDB(1, 2, 3, 4, 5, 6)
	db.Articles["two/a.md"] = Article{SourceID: "two", Path: "a.md", Versions: revisions(1), ReconcileBase: &ReconcileBase{SourceID: "one", Path: "a.md", Revision: 2, SHA: "sha2"}}
	PruneAll(&db)
	expectRevisions(t, db, "one/a.md", 2, 4, 5, 6)
	target := db.Articles["two/a.md"]
	target.ReconcileBase.Revision, target.ReconcileBase.SHA = 6, "sha6"
	db.Articles["two/a.md"] = target
	PruneAll(&db)
	expectRevisions(t, db, "one/a.md", 4, 5, 6)
}

func TestPruneDeletedArticleUsesTheSameRule(t *testing.T) {
	db := pruneDB(1, 2, 3, 4, 5)
	a := db.Articles["one/a.md"]
	a.Deleted = true
	db.Articles["one/a.md"] = a
	db.Outbox["one/a.md@2"] = SyncJob{ID: "one/a.md@2", SourceID: "one", Path: "a.md", Revision: 2, SHA: "sha2", State: "retry"}
	PruneArticle(&db, "one/a.md")
	expectRevisions(t, db, "one/a.md", 2, 3, 4, 5)
	if !db.Articles["one/a.md"].Deleted {
		t.Fatal("deletion lost")
	}
}

func TestPublishedSnapshotSurvivesPruningOfItsRevision(t *testing.T) {
	db := pruneDB(1, 2, 3, 4, 5)
	a := db.Articles["one/a.md"]
	published := a.Versions[0]
	published.Number = 0
	a.Published = &published
	db.Articles["one/a.md"] = a
	if len(ProtectedRevisions(db, "one/a.md")) != 0 {
		t.Fatal("publication protected a revision")
	}
	if removed := PruneAll(&db); removed != 2 {
		t.Fatal("removed", removed)
	}
	expectRevisions(t, db, "one/a.md", 3, 4, 5)
	if p := db.Articles["one/a.md"].Published; p == nil || p.Text != "# 1" || p.SHA != "sha1" {
		t.Fatal("publication snapshot lost", p)
	}
}

func TestCopyOriginDoesNotProtect(t *testing.T) {
	db := pruneDB(1, 2, 3, 4, 5)
	db.Articles["two/a.md"] = Article{SourceID: "two", Path: "a.md", Versions: revisions(1), CopiedFrom: &CopyOrigin{SourceID: "one", Path: "a.md", Revision: 1, SHA: "sha1"}}
	PruneAll(&db)
	expectRevisions(t, db, "one/a.md", 3, 4, 5)
}

func connectedDB(versions ...int) Database {
	db := pruneDB(versions...)
	db.Sources["one"] = Source{ID: "one", Kind: "wiki", ConnectionID: "conn"}
	return db
}

func TestUpstreamBaseOfAConnectedArticleIsProtected(t *testing.T) {
	db := connectedDB(1, 2, 3, 4, 5, 6)
	a := db.Articles["one/a.md"]
	a.UpstreamHash = "sha2"
	db.Articles["one/a.md"] = a
	if got := ProtectedRevisions(db, "one/a.md"); !reflect.DeepEqual(got, map[int]bool{2: true}) {
		t.Fatal("protected", got)
	}
	PruneArticle(&db, "one/a.md")
	expectRevisions(t, db, "one/a.md", 2, 4, 5, 6)

	// The newest revision with that SHA is the one kept.
	db = connectedDB(1, 2, 3, 4, 5, 6)
	a = db.Articles["one/a.md"]
	versions := append([]Revision(nil), a.Versions...)
	versions[2].SHA = "sha1"
	a.Versions, a.UpstreamHash = versions, "sha1"
	db.Articles["one/a.md"] = a
	if got := ProtectedRevisions(db, "one/a.md"); !reflect.DeepEqual(got, map[int]bool{3: true}) {
		t.Fatal("protected", got)
	}

	db = pruneDB(1, 2, 3, 4, 5, 6)
	a = db.Articles["one/a.md"]
	a.UpstreamHash = "sha2"
	db.Articles["one/a.md"] = a
	PruneArticle(&db, "one/a.md")
	expectRevisions(t, db, "one/a.md", 4, 5, 6)
}

func TestRevisionProtectedBySeveralRecords(t *testing.T) {
	db := connectedDB(1, 2, 3, 4, 5, 6)
	a := db.Articles["one/a.md"]
	a.UpstreamHash = "sha1"
	db.Articles["one/a.md"] = a
	db.Articles["two/a.md"] = Article{SourceID: "two", Path: "a.md", Versions: revisions(1), ReconcileBase: &ReconcileBase{SourceID: "one", Path: "a.md", Revision: 1, SHA: "sha1"}}
	db.Outbox["one/a.md@1"] = SyncJob{ID: "one/a.md@1", SourceID: "one", Path: "a.md", Revision: 1, SHA: "sha1", State: "pending"}
	db.Moves["m"] = Move{ID: "m", SourceID: "one", SourcePath: "a.md", SourceRevision: 1, DestinationID: "two", DestinationPath: "a.md", SHA: "sha1", State: "waiting_destination"}
	if got := ProtectedRevisions(db, "one/a.md"); !reflect.DeepEqual(got, map[int]bool{1: true}) {
		t.Fatal("protected", got)
	}
	if removed := PruneAll(&db); removed != 2 {
		t.Fatal("removed", removed)
	}
	expectRevisions(t, db, "one/a.md", 1, 4, 5, 6)
	// Lifting all but one protection still keeps the revision.
	job := db.Outbox["one/a.md@1"]
	job.State = "complete"
	db.Outbox["one/a.md@1"] = job
	move := db.Moves["m"]
	move.State = "cancelled"
	db.Moves["m"] = move
	a = db.Articles["one/a.md"]
	a.UpstreamHash = "sha6"
	db.Articles["one/a.md"] = a
	PruneAll(&db)
	expectRevisions(t, db, "one/a.md", 1, 4, 5, 6)
	target := db.Articles["two/a.md"]
	target.ReconcileBase = nil
	db.Articles["two/a.md"] = target
	PruneAll(&db)
	expectRevisions(t, db, "one/a.md", 4, 5, 6)
}

func TestPruneAllKeepsTheReconcileBaseOfADeletedArticle(t *testing.T) {
	db := pruneDB(1, 2, 3, 4, 5, 6)
	a := db.Articles["one/a.md"]
	a.Deleted = true
	db.Articles["one/a.md"] = a
	db.Articles["two/a.md"] = Article{SourceID: "two", Path: "a.md", Versions: revisions(1), ReconcileBase: &ReconcileBase{SourceID: "one", Path: "a.md", Revision: 2, SHA: "sha2"}}
	if removed := PruneAll(&db); removed != 2 {
		t.Fatal("removed", removed)
	}
	expectRevisions(t, db, "one/a.md", 2, 4, 5, 6)
	if !db.Articles["one/a.md"].Deleted {
		t.Fatal("deletion lost")
	}
}
