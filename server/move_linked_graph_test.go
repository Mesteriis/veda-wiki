package main

import (
	"context"
	"testing"
)

func TestMoveLinkedGraphToLocalIsAtomic(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "[A](a.md)", 0)
	move, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "complete" || !s.db.Articles["from/a.md"].Deleted || s.db.Articles["from/b.md"].Deleted || s.db.Articles["to/b.md"].CopiedFrom == nil {
		t.Fatal(move, err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Version != 8 || len(reopened.moves()[0].Links) != 1 {
		t.Fatal("move not durable", err)
	}
}
func TestRemoteMoveLinkedGraphKeepsSourceIfDestinationChanges(t *testing.T) {
	s := testStore(t)
	source(t, s, "to", "private")
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "README.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatal(move, err)
	}
	if _, err = s.put("to", "b.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if f.contents()["README.md"] == "" || s.db.Articles["handbook/README.md"].Deleted {
		t.Fatal("source removed after dependency changed")
	}
}

func TestRemoteMoveLinkedGraphDeletesOnlyRequestedArticle(t *testing.T) {
	s := testStore(t)
	source(t, s, "to", "private")
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	manager := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "README.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "waiting_source_delete" {
		t.Fatal(move, err)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || f.contents()["README.md"] != "" || f.contents()["b.md"] != "# B" || s.db.Articles["to/b.md"].Deleted {
		t.Fatal("unexpected remote deletion", s.moves())
	}
}
