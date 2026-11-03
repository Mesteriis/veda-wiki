package syncer

import (
	"context"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
	"github.com/Mesteriis/veda-wiki/internal/translation"
)

func TestMoveBetweenConnectedRepositoriesWaitsForObservation(t *testing.T) {
	s, manager, original, destination := connectedMoveFixtures(t)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_destination" {
		t.Fatalf("move: %#v %v", move, err)
	}
	syncFixtureSource(t, manager, "two")
	syncFixtureSource(t, manager, "one")
	if original.Contents()["README.md"] != "# Source" || original.WriteCount() != 0 || s.moves()[0].State != "waiting_destination" {
		t.Fatal("source deleted before destination observation")
	}
	syncFixtureSource(t, manager, "two")
	if s.moves()[0].State != "waiting_source_delete" || destination.Contents()["moved.md"] != "# Source" {
		t.Fatal("destination not confirmed", s.moves())
	}
	syncFixtureSource(t, manager, "one")
	if s.moves()[0].State != "deleting_source" {
		t.Fatal("delete not prepared", s.moves())
	}
	syncFixtureSource(t, manager, "one")
	if s.moves()[0].State != "complete" || original.WriteCount() != 1 || destination.WriteCount() != 1 {
		t.Fatal("move not completed", s.moves())
	}
	if _, exists := original.Contents()["README.md"]; exists {
		t.Fatal("original still exists")
	}
	if destination.Contents()["moved.md"] != "# Source" {
		t.Fatal("destination lost")
	}
}

func TestMoveDoesNotDeleteSourceWhenDestinationUnavailableOrChanged(t *testing.T) {
	for _, outage := range []bool{true, false} {
		s, manager, original, destination := connectedMoveFixtures(t)
		_, err := s.moveArticle(transfer.MoveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
		if err != nil {
			t.Fatal(err)
		}
		syncFixtureSource(t, manager, "two")
		syncFixtureSource(t, manager, "two")
		if s.moves()[0].State != "waiting_source_delete" {
			t.Fatal(s.moves())
		}
		if outage {
			destination.Modify(func() { destination.Fail = true })
		} else {
			destination.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination", "moved.md": "# External change"})
		}
		syncFixtureSource(t, manager, "one")
		if original.WriteCount() != 0 || original.Contents()["README.md"] != "# Source" {
			t.Fatal("source deleted without checking destination")
		}
	}
}

func TestMoveRejectsAliasedRepositoryAndSamePhysicalPath(t *testing.T) {
	s, _, original, _ := connectedMoveFixtures(t)
	// Model a stale second source view of the same repository and branch.
	s.mutate(func(db *domain.Database) {
		alias := db.Connections["two"]
		alias.RepositoryID = db.Connections["one"].RepositoryID
		alias.Branch = db.Connections["one"].Branch
		db.Connections["two"] = alias
		delete(db.Articles, "two/README.md")
	})
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "README.md", ConfirmMove: true})
	if err != domain.ErrConflict {
		t.Fatalf("aliased path accepted: %v", err)
	}
	if original.Contents()["README.md"] != "# Source" || original.WriteCount() != 0 {
		t.Fatal("original changed")
	}
}

func TestUnavailableDestinationDoesNotBlockOtherSourceMoves(t *testing.T) {
	s, manager, original, destination := connectedMoveFixtures(t)
	original.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source", "other.md": "# Other"})
	syncFixtureSource(t, manager, "one")
	source(t, s, "local", "private")
	first, err := s.moveArticle(transfer.MoveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	syncFixtureSource(t, manager, "two")
	syncFixtureSource(t, manager, "two")
	second, err := s.moveArticle(transfer.MoveRequest{SourceID: "one", SourcePath: "other.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "other.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	destination.Modify(func() { destination.Fail = true })
	syncFixtureSource(t, manager, "one")
	firstState, secondState := s.db().Moves[first.ID].State, s.db().Moves[second.ID].State
	if firstState != "waiting_source_delete" || secondState != "deleting_source" {
		t.Fatalf("unexpected move states: %s %s", firstState, secondState)
	}
	files := original.Contents()
	if files["README.md"] != "# Source" {
		t.Fatal("unavailable destination lost source")
	}
	if _, exists := files["other.md"]; exists {
		t.Fatal("unrelated move did not advance")
	}
}

func TestUnavailableMoveDestinationDoesNotBlockUnrelatedWrites(t *testing.T) {
	s, manager, original, destination := connectedMoveFixtures(t)
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	syncFixtureSource(t, manager, "two")
	syncFixtureSource(t, manager, "two")
	if _, err := s.put("one", "other.md", "# Independent", 0); err != nil {
		t.Fatal(err)
	}
	destination.Modify(func() { destination.Fail = true })
	syncFixtureSource(t, manager, "one")
	files := original.Contents()
	if files["README.md"] != "# Source" || files["other.md"] != "# Independent" {
		t.Fatal("move outage blocked unrelated write or deleted source")
	}
	if s.moves()[0].State != "waiting_source_delete" {
		t.Fatal("move unexpectedly advanced", s.moves())
	}
}

func TestMoveWaitsForObservedRemoteDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Local", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	m := connectFixture(t, s, f, true)
	req := transfer.MoveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	move, e := s.moveArticle(req)
	if e != nil || move.State != "waiting_destination" {
		t.Fatal(move, e)
	}
	f.Modify(func() { f.Fail = true })
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil {
		t.Fatal("source lost during outage", e)
	}
	f.Modify(func() { f.Fail = false })
	if e = s.retryConnection("handbook"); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil {
		t.Fatal("source removed before observation", e)
	}
	s, e = s.reopen()
	if e != nil {
		t.Fatal("restart pending move", e)
	}
	m = newSyncManager(s, f.Client())
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != domain.ErrNotFound || s.moves()[0].State != "complete" || f.Contents()["moved.md"] != "# Local" {
		t.Fatal("move did not finish after observation", e, s.moves())
	}
	reopened, e := s.reopen()
	if e != nil || reopened.moves()[0].State != "complete" {
		t.Fatal("move not durable", e)
	}
}

func TestMoveConcurrentEditsKeepSource(t *testing.T) {
	for _, side := range []string{"source", "destination"} {
		s := testStore(t)
		source(t, s, "local", "private")
		s.put("local", "a.md", "# Local", 0)
		f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
		m := connectFixture(t, s, f, true)
		req := transfer.MoveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
		if _, e := s.moveArticle(req); e != nil {
			t.Fatal(e)
		}
		if side == "source" {
			if _, e := s.put("local", "a.md", "# Edited source", 1); e != nil {
				t.Fatal(e)
			}
		} else {
			if _, e := s.put("handbook", "moved.md", "# Edited destination", 1); e != nil {
				t.Fatal(e)
			}
		}
		m.Once(context.Background())
		m.Once(context.Background())
		if _, e := s.article("local", "a.md", false); e != nil || s.moves()[0].State != side+"_changed" {
			t.Fatal("concurrent edit lost source", side, e, s.moves())
		}
	}
}

func TestMoveReadOnlyDestinationRetainsSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if _, e := s.put("local", "a.md", "# Source", 0); e != nil {
		t.Fatal(e)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, false)
	req := transfer.MoveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e := s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	if _, e := s.article("local", "a.md", false); e != nil || s.moves()[0].State != "waiting_destination" || f.WriteCount() != 0 {
		t.Fatal("read-only destination removed source", e)
	}
}

func TestMoveRejectsTranslationsAndDestinationIdentityChange(t *testing.T) {
	s := testStore(t)
	if e := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	source(t, s, "target", "private")
	rev, e := s.put("program", "docs/a.md", "# Original", 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.translation().Submit(translation.Request{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); e != nil {
		t.Fatal(e)
	}
	req := transfer.MoveRequest{SourceID: "program", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "a.md", ConfirmMove: true}
	if _, e = s.moveArticle(req); e != domain.ErrInvalid {
		t.Fatal("translated article moved without translation", e)
	}
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Local", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	connectFixture(t, s, f, true)
	req = transfer.MoveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e = s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	db := s.db()
	dst := db.Sources["handbook"]
	dst.ConnectionID = ""
	db.Sources[dst.ID] = dst
	domain.ReconcileMoves(&db)
	for _, m := range db.Moves {
		if m.State != "destination_changed" {
			t.Fatal("changed destination identity accepted", m)
		}
	}
	if db.Articles["local/a.md"].Deleted {
		t.Fatal("source removed after destination identity changed")
	}
}

func TestMoveRecoversLostRemoteResponseBeforeRemovingSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if _, e := s.put("local", "a.md", "# Source", 0); e != nil {
		t.Fatal(e)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := transfer.MoveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e := s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	f.Modify(func() { f.LoseResponse = true })
	manager.Once(context.Background())
	if f.WriteCount() != 1 {
		t.Fatal("remote fixture did not apply commit")
	}
	if _, e := s.article("local", "a.md", false); e != nil {
		t.Fatal("source removed on ambiguous response", e)
	}
	reopened, e := s.reopen()
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.retryConnection("handbook"); e != nil {
		t.Fatal(e)
	}
	manager = newSyncManager(reopened, f.Client())
	manager.Once(context.Background())
	if _, e = reopened.article("local", "a.md", false); e != domain.ErrNotFound || reopened.moves()[0].State != "complete" || f.WriteCount() != 1 {
		t.Fatal("lost response recovery failed", e, reopened.moves())
	}
}

func TestCancelWaitingMoveRetainsBothCopies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Source", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := transfer.MoveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	m, e := s.moveArticle(req)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.cancelMove(m.ID, false); e != domain.ErrInvalid {
		t.Fatal("cancel without confirmation", e)
	}
	if e = s.cancelMove(m.ID, true); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil || s.moves()[0].State != "cancelled" || f.Contents()["moved.md"] != "# Source" {
		t.Fatal("cancel did not retain copies", e, s.moves())
	}
	reopened, e := s.reopen()
	if e != nil || reopened.moves()[0].State != "cancelled" {
		t.Fatal("cancel not durable", e)
	}
}

func TestMoveKeepsSourceWhenTranslationIsAddedWhileWaiting(t *testing.T) {
	s := testStore(t)
	if e := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	rev, e := s.put("program", "docs/a.md", "# Original", 0)
	if e != nil {
		t.Fatal(e)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := transfer.MoveRequest{SourceID: "program", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e = s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	if _, e = s.translation().Submit(translation.Request{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	a, e := s.article("program", "docs/a.md", false)
	if e != nil || a.Translation == nil || s.moves()[0].State != "source_changed" {
		t.Fatal("translation was lost", e, s.moves())
	}
}
