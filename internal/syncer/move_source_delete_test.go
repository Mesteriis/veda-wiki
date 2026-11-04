package syncer

import (
	"context"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

func TestUncertainDeleteRefreshesBranchBeforeAnotherMove(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# First", "other.md": "# Second"})
	manager := connectFixture(t, s, f, true)
	first, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "first.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "other.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "second.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	manager.Once(context.Background())
	firstState, secondState := s.db().Moves[first.ID].State, s.db().Moves[second.ID].State
	if firstState != "deleting_source" || secondState != "waiting_source_delete" || f.WriteCount() != 1 {
		t.Fatal("uncertain attempt did not stop same-head writes")
	}
	if err := s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	firstState, secondState = s.db().Moves[first.ID].State, s.db().Moves[second.ID].State
	if firstState != "complete" || secondState != "complete" || f.WriteCount() != 2 {
		t.Fatalf("moves did not recover: %s %s", firstState, secondState)
	}
}

func TestConnectedSourceMoveWaitsForRemoteDeletion(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatalf("move: %#v %v", move, err)
	}
	if _, err := s.article("handbook", "README.md", false); err != nil || f.Contents()["README.md"] != "# Source" {
		t.Fatal("source was deleted before remote deletion", err)
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Version != domain.RevisionsFormat || reopened.moves()[0].SourceRepositoryID != f.RepoID {
		t.Fatal("move state was not durable", err)
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Edited externally"})
	manager.Once(context.Background())
	if s.moves()[0].State != "source_changed" {
		t.Fatal("upstream edit did not block deletion", s.moves())
	}
	if got, err := s.article("handbook", "README.md", false); err != nil || got.Versions[len(got.Versions)-1].Text != "# Edited externally" {
		t.Fatal("upstream edit lost", err)
	}
}

func TestConnectedSourceMoveCancellationKeepsCopies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cancelMove(move.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.article("handbook", "README.md", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.article("local", "copy.md", false); err != nil {
		t.Fatal(err)
	}
	if f.Contents()["README.md"] != "# Source" {
		t.Fatal("remote source deleted")
	}
}

func TestConnectedSourceMoveRejectsReadOnlyOrUnsyncedSource(t *testing.T) {
	for _, write := range []bool{false, true} {
		s := testStore(t)
		source(t, s, "local", "private")
		f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
		connectFixture(t, s, f, write)
		if write {
			if _, err := s.put("handbook", "README.md", "# Local edit", 1); err != nil {
				t.Fatal(err)
			}
		}
		expected := 1
		if write {
			expected = 2
		}
		_, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: expected, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
		if err != domain.ErrConflict {
			t.Fatalf("write=%v: expected conflict, got %v", write, err)
		}
		if _, err := s.article("local", "copy.md", false); err != domain.ErrNotFound {
			t.Fatal("unexpected destination", err)
		}
	}
}

func TestConnectedSourceMoveRecoversLostDeleteResponse(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source", "other.md": "# Retain"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	manager.Once(context.Background())
	if s.moves()[0].State != "deleting_source" || s.moves()[0].PreparedDeletion == "" {
		t.Fatal("prepared delete not recorded", s.moves())
	}
	if _, err := s.article("handbook", "README.md", false); err != nil {
		t.Fatal("source hidden before observation", err)
	}
	if _, err := s.put("local", "copy.md", "# Changed", 1); err != domain.ErrConflict {
		t.Fatal("destination not locked", err)
	}
	if err := s.cancelMove(move.ID, true); err != domain.ErrConflict {
		t.Fatal("uncertain deletion was cancelled", err)
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	manager = newSyncManager(reopened, f.Client())
	manager.Once(context.Background())
	if reopened.moves()[0].State != "complete" {
		t.Fatal("deletion not observed", reopened.moves())
	}
	if _, err := reopened.article("handbook", "README.md", false); err != domain.ErrNotFound {
		t.Fatal("source still present", err)
	}
	if got, err := reopened.article("local", "copy.md", false); err != nil || got.Versions[0].Text != "# Source" {
		t.Fatal("destination lost", err)
	}
	files := f.Contents()
	if _, exists := files["README.md"]; exists || files["other.md"] != "# Retain" || f.WriteCount() != 1 {
		t.Fatal("unexpected remote changes")
	}
}

func TestConnectedSourceMoveCanCancelAfterDefinitiveRejection(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.Reject = true })
	manager.Once(context.Background())
	if got := s.moves()[0]; got.State != "waiting_source_delete" || got.PreparedDeletion != "" {
		t.Fatal("definitive rejection left source locked", got)
	}
	if err := s.cancelMove(move.ID, true); err != nil {
		t.Fatal(err)
	}
	if f.Contents()["README.md"] != "# Source" || f.WriteCount() != 0 {
		t.Fatal("source changed after rejected update")
	}
}

func TestConnectedSourceMoveUncertainAttemptRemainsLockedAfterRejection(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Source"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "local", DestinationPath: "copy.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a prepared request whose outcome could not be determined after restart.
	s.mutate(func(db *domain.Database) {
		move.State, move.PreparedDeletion = "deleting_source", strings.Repeat("a", 40)
		db.Moves[move.ID] = move
	})
	f.Modify(func() { f.Reject = true })
	manager.Once(context.Background())
	if s.moves()[0].State != "deleting_source" {
		t.Fatal("uncertain prior request was unlocked", s.moves())
	}
	if err := s.cancelMove(move.ID, true); err != domain.ErrConflict {
		t.Fatal("uncertain move cancelled", err)
	}
}
