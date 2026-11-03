package main

import (
	"context"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"testing"
)

func TestCopyUpdatesExistingDestinationWithRevisionGuards(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if _, err := s.put("local", "guide.md", "# New", 0); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Old"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	req := copyRequest{SourceID: "local", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "guide.md"}
	if _, err := s.copyArticle(req); !errors.Is(err, ErrConflict) {
		t.Fatal("implicit overwrite accepted", err)
	}
	stale := 2
	req.ExpectedDestinationRevision = &stale
	if _, err := s.copyArticle(req); !errors.Is(err, ErrConflict) {
		t.Fatal("stale destination accepted", err)
	}
	expected := 1
	req.ExpectedDestinationRevision = &expected
	rev, err := s.copyArticle(req)
	if err != nil {
		t.Fatal(err)
	}
	a := s.DB().Articles["handbook/guide.md"]
	if rev.Number != 2 || len(a.Versions) != 2 || a.Versions[0].Text != "# Old" || a.CopiedFrom == nil || a.CopiedFrom.SourceID != "local" || a.UpstreamHash != digest([]byte("# Old")) {
		t.Fatal("history or provenance not preserved")
	}
	if len(s.jobs()) != 1 || s.jobs()[0].Revision != 2 {
		t.Fatal("new revision was not queued")
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.Contents()["guide.md"] != "# New" || s.jobs()[0].State != "complete" {
		t.Fatal("copy update was not confirmed")
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.DB().Articles["handbook/guide.md"].Versions) != 2 || reopened.DB().Articles["local/guide.md"].Deleted {
		t.Fatal("state did not survive restart")
	}
}

func TestCopyUpdateQueuesImageWithExistingDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	ref := fixtureAsset(t, s, "local", "img/icon.png")
	text := "# Updated\n\n![icon](img/icon.png)"
	if _, err := s.put("local", "guide.md", text, 0); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Old"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	expected := 1
	req := copyRequest{SourceID: "local", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "guide.md", ExpectedDestinationRevision: &expected}
	if _, err := s.copyArticle(req); err != nil {
		t.Fatal(err)
	}
	jobs := s.jobs()
	if len(jobs) != 1 || jobs[0].Assets["img/icon.png"] != ref || jobs[0].Revision != 2 {
		t.Fatal("image not attached to updated revision")
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.Contents()["guide.md"] != text || digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA || s.jobs()[0].State != "complete" {
		t.Fatal("update and image were not confirmed")
	}
}

func TestCopyUpdatePreservesPendingImage(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	ref := fixtureAsset(t, s, "local", "img/icon.png")
	if _, err := s.put("local", "guide.md", "# First\n![icon](img/icon.png)", 0); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Root"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	req := copyRequest{SourceID: "local", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "guide.md"}
	if _, err := s.copyArticle(req); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("local", "guide.md", "# Second\n![icon](img/icon.png)", 1); err != nil {
		t.Fatal(err)
	}
	expected := 1
	req.ExpectedRevision = 2
	req.ExpectedDestinationRevision = &expected
	if _, err := s.copyArticle(req); err != nil {
		t.Fatal(err)
	}
	jobs := s.jobs()
	var pending *SyncJob
	for i := range jobs {
		if jobs[i].State == "pending" {
			pending = &jobs[i]
		}
	}
	if pending == nil || pending.Revision != 2 || pending.Assets["img/icon.png"] != ref {
		t.Fatal("pending image lost")
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.Contents()["guide.md"] != "# Second\n![icon](img/icon.png)" || digest([]byte(f.Contents()["img/icon.png"])) != ref.SHA {
		t.Fatal("latest pending bundle not synchronized")
	}
}

func TestCopyUpdateOwnerAPI(t *testing.T) {
	s := testStore(t)
	source(t, s, "a", "private")
	source(t, s, "b", "private")
	if _, err := s.put("a", "x.md", "# From", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("b", "x.md", "# Before", 0); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	body := `{"source_id":"a","source_path":"x.md","expected_revision":1,"destination_id":"b","destination_path":"x.md","expected_destination_revision":1}`
	if r := request(w, "POST", "/api/v1/articles/copy", body, false); r.Code != 403 {
		t.Fatal("unauthorized copy", r.Code)
	}
	if r := request(w, "POST", "/api/v1/articles/copy", body, true); r.Code != 201 {
		t.Fatal("copy update failed", r.Code)
	}
	if r := request(w, "POST", "/api/v1/articles/copy", body, true); r.Code != 409 {
		t.Fatal("stale API copy accepted", r.Code)
	}
}

func TestCopyUpdateReconcilesWaitingMove(t *testing.T) {
	s, _, _, _ := connectedMoveFixtures(t)
	move, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil || move.State != "waiting_destination" {
		t.Fatal("move fixture failed", err)
	}
	source(t, s, "local", "private")
	if _, err = s.put("local", "new.md", "# Replacement", 0); err != nil {
		t.Fatal(err)
	}
	expected := 1
	if _, err = s.copyArticle(copyRequest{SourceID: "local", SourcePath: "new.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "moved.md", ExpectedDestinationRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	if s.DB().Moves[move.ID].State != "destination_changed" || s.DB().Articles["one/README.md"].Deleted {
		t.Fatal("move was not reconciled safely")
	}
}
