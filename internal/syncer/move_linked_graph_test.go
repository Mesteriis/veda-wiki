package syncer

import (
	"context"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

func TestPreparedRemoteDeletionLocksLinkedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "to", "private")
	source(t, s, "other", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "README.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil {
		t.Fatal(err)
	}
	s.mutate(func(db *domain.Database) {
		move.State, move.PreparedDeletion = "deleting_source", strings.Repeat("a", 40)
		db.Moves[move.ID] = move
	})
	if _, err = s.put("to", "b.md", "# Changed", 1); err != domain.ErrConflict {
		t.Fatal("linked edit was not locked", err)
	}
	if _, err = s.moveArticle(transfer.MoveRequest{SourceID: "to", SourcePath: "b.md", ExpectedRevision: 1, DestinationID: "other", DestinationPath: "b.md", ConfirmMove: true}); err != domain.ErrConflict {
		t.Fatal("linked move was not locked", err)
	}
	if s.db().Articles["to/b.md"].Deleted || len(s.db().Articles["to/b.md"].Versions) != 1 {
		t.Fatal("linked article changed")
	}
	s.mutate(func(db *domain.Database) {
		move.State = "complete"
		db.Moves[move.ID] = move
	})
	if _, err = s.put("to", "b.md", "# Changed", 1); err != nil {
		t.Fatal("completed move kept link locked", err)
	}
}
func TestRemoteMoveLinkedGraphKeepsSourceIfDestinationChanges(t *testing.T) {
	s := testStore(t)
	source(t, s, "to", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "README.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatal(move, err)
	}
	if _, err = s.put("to", "b.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.Contents()["README.md"] == "" || s.db().Articles["handbook/README.md"].Deleted {
		t.Fatal("source removed after dependency changed")
	}
}

func TestRemoteMoveLinkedGraphDeletesOnlyRequestedArticle(t *testing.T) {
	s := testStore(t)
	source(t, s, "to", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(transfer.MoveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "README.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatal(move, err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || f.Contents()["README.md"] != "" || f.Contents()["b.md"] != "# B" || s.db().Articles["to/b.md"].Deleted {
		t.Fatal("unexpected remote deletion", s.moves())
	}
}
