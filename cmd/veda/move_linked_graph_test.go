package main

import (
	"context"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"strings"
	"testing"
)

func TestMoveLinkedGraphToLocalIsAtomic(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "[A](a.md)", 0)
	move, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "complete" || !s.DB().Articles["from/a.md"].Deleted || s.DB().Articles["from/b.md"].Deleted || s.DB().Articles["to/b.md"].CopiedFrom == nil {
		t.Fatal(move, err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil || reopened.DB().Version != 8 || len(reopened.moves()[0].Links) != 1 {
		t.Fatal("move not durable", err)
	}
}

func TestPreparedRemoteDeletionLocksLinkedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "to", "private")
	source(t, s, "other", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "README.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil {
		t.Fatal(err)
	}
	s.Lock()
	db := s.Clone()
	move.State, move.PreparedDeletion = "deleting_source", strings.Repeat("a", 40)
	db.Moves[move.ID] = move
	err = s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("to", "b.md", "# Changed", 1); err != ErrConflict {
		t.Fatal("linked edit was not locked", err)
	}
	if _, err = s.moveArticle(moveRequest{SourceID: "to", SourcePath: "b.md", ExpectedRevision: 1, DestinationID: "other", DestinationPath: "b.md", ConfirmMove: true}); err != ErrConflict {
		t.Fatal("linked move was not locked", err)
	}
	if s.DB().Articles["to/b.md"].Deleted || len(s.DB().Articles["to/b.md"].Versions) != 1 {
		t.Fatal("linked article changed")
	}
	s.Lock()
	db = s.Clone()
	move.State = "complete"
	db.Moves[move.ID] = move
	err = s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("to", "b.md", "# Changed", 1); err != nil {
		t.Fatal("completed move kept link locked", err)
	}
}
func TestRemoteMoveLinkedGraphKeepsSourceIfDestinationChanges(t *testing.T) {
	s := testStore(t)
	source(t, s, "to", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "README.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatal(move, err)
	}
	if _, err = s.put("to", "b.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.Contents()["README.md"] == "" || s.DB().Articles["handbook/README.md"].Deleted {
		t.Fatal("source removed after dependency changed")
	}
}

func TestRemoteMoveLinkedGraphDeletesOnlyRequestedArticle(t *testing.T) {
	s := testStore(t)
	source(t, s, "to", "private")
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "README.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatal(move, err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || f.Contents()["README.md"] != "" || f.Contents()["b.md"] != "# B" || s.DB().Articles["to/b.md"].Deleted {
		t.Fatal("unexpected remote deletion", s.moves())
	}
}
