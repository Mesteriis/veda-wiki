package main

import (
	"context"
	"testing"
)

func TestMoveLocalAtomicAndRetainsHistory(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	if _, e := s.put("from", "a.md", "# Local", 0); e != nil {
		t.Fatal(e)
	}
	req := moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md"}
	if _, e := s.moveArticle(req); e != ErrInvalid {
		t.Fatal("move without consent", e)
	}
	req.ConfirmMove = true
	m, e := s.moveArticle(req)
	if e != nil || m.State != "complete" {
		t.Fatal(m, e)
	}
	if _, e := s.article("from", "a.md", false); e != ErrNotFound {
		t.Fatal("source still visible", e)
	}
	target, e := s.article("to", "a.md", false)
	if e != nil || target.Versions[0].Text != "# Local" || target.CopiedFrom == nil {
		t.Fatal("destination missing", e)
	}
	reopened, e := openStore(s.dir)
	if e != nil || reopened.db.Version != 2 || !reopened.db.Articles["from/a.md"].Deleted || len(reopened.db.Articles["from/a.md"].Versions) != 1 || reopened.moves()[0].State != "complete" {
		t.Fatal("move history not durable", e)
	}
}
func TestMoveWaitsForObservedRemoteDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Local", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	m := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	move, e := s.moveArticle(req)
	if e != nil || move.State != "waiting_destination" {
		t.Fatal(move, e)
	}
	f.modify(func() { f.fail = true })
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil {
		t.Fatal("source lost during outage", e)
	}
	f.modify(func() { f.fail = false })
	if e = s.retryConnection("handbook"); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil {
		t.Fatal("source removed before observation", e)
	}
	s, e = openStore(s.dir)
	if e != nil {
		t.Fatal("restart pending move", e)
	}
	m = newSyncManager(s, f.client())
	m.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != ErrNotFound || s.moves()[0].State != "complete" || f.contents()["moved.md"] != "# Local" {
		t.Fatal("move did not finish after observation", e, s.moves())
	}
	reopened, e := openStore(s.dir)
	if e != nil || reopened.moves()[0].State != "complete" {
		t.Fatal("move not durable", e)
	}
}
func TestMoveConcurrentEditsKeepSource(t *testing.T) {
	for _, side := range []string{"source", "destination"} {
		s := testStore(t)
		source(t, s, "local", "private")
		s.put("local", "a.md", "# Local", 0)
		f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
		m := connectFixture(t, s, f, true)
		req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
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
func TestMoveRejectsImportedSourcesAndRelativeDependencies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	source(t, s, "target", "private")
	for _, item := range []struct{ path, text string }{{"image.md", "# Doc\n![image](assets/a.png)"}, {"link.md", "# Doc\n[guide](../guide.md)"}} {
		if _, e := s.put("local", item.path, item.text, 0); e != nil {
			t.Fatal(e)
		}
		req := moveRequest{SourceID: "local", SourcePath: item.path, ExpectedRevision: 1, DestinationID: "target", DestinationPath: item.path, ConfirmMove: true}
		if _, e := s.moveArticle(req); e != ErrInvalid {
			t.Fatal("relative dependency accepted", e)
		}
	}
	batch := testBatch()
	if _, e := s.importBatch(batch); e != nil {
		t.Fatal(e)
	}
	req := moveRequest{SourceID: "handbook", SourcePath: "README.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "x.md", ConfirmMove: true}
	if _, e := s.moveArticle(req); e != ErrInvalid {
		t.Fatal("imported source accepted", e)
	}
	w := Wiki{Store: s, Token: "test-owner"}
	if r := request(w, "GET", "/api/v1/moves", "", false); r.Code != 404 {
		t.Fatal("move metadata exposed", r.Code)
	}
}

func TestMoveReadOnlyDestinationRetainsSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if _, e := s.put("local", "a.md", "# Source", 0); e != nil {
		t.Fatal(e)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, false)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e := s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	if _, e := s.article("local", "a.md", false); e != nil || s.moves()[0].State != "waiting_destination" || f.writeCount() != 0 {
		t.Fatal("read-only destination removed source", e)
	}
}

func TestMoveRejectsTranslationsAndDestinationIdentityChange(t *testing.T) {
	s := testStore(t)
	if e := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	source(t, s, "target", "private")
	rev, e := s.put("program", "docs/a.md", "# Original", 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.submitTranslation(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); e != nil {
		t.Fatal(e)
	}
	req := moveRequest{SourceID: "program", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "a.md", ConfirmMove: true}
	if _, e = s.moveArticle(req); e != ErrInvalid {
		t.Fatal("translated article moved without translation", e)
	}
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Local", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	connectFixture(t, s, f, true)
	req = moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e = s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	db := s.clone()
	dst := db.Sources["handbook"]
	dst.ConnectionID = ""
	db.Sources[dst.ID] = dst
	reconcileMoves(&db)
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
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e := s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	f.modify(func() { f.loseResponse = true })
	manager.Once(context.Background())
	if f.writeCount() != 1 {
		t.Fatal("remote fixture did not apply commit")
	}
	if _, e := s.article("local", "a.md", false); e != nil {
		t.Fatal("source removed on ambiguous response", e)
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.retryConnection("handbook"); e != nil {
		t.Fatal(e)
	}
	manager = newSyncManager(reopened, f.client())
	manager.Once(context.Background())
	if _, e = reopened.article("local", "a.md", false); e != ErrNotFound || reopened.moves()[0].State != "complete" || f.writeCount() != 1 {
		t.Fatal("lost response recovery failed", e, reopened.moves())
	}
}

func TestCancelWaitingMoveRetainsBothCopies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Source", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	m, e := s.moveArticle(req)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.cancelMove(m.ID, false); e != ErrInvalid {
		t.Fatal("cancel without confirmation", e)
	}
	if e = s.cancelMove(m.ID, true); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	if _, e = s.article("local", "a.md", false); e != nil || s.moves()[0].State != "cancelled" || f.contents()["moved.md"] != "# Source" {
		t.Fatal("cancel did not retain copies", e, s.moves())
	}
	reopened, e := openStore(s.dir)
	if e != nil || reopened.moves()[0].State != "cancelled" {
		t.Fatal("cancel not durable", e)
	}
}

func TestMoveKeepsSourceWhenTranslationIsAddedWhileWaiting(t *testing.T) {
	s := testStore(t)
	if e := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	rev, e := s.put("program", "docs/a.md", "# Original", 0)
	if e != nil {
		t.Fatal(e)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Existing"})
	manager := connectFixture(t, s, f, true)
	req := moveRequest{SourceID: "program", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "moved.md", ConfirmMove: true, ConfirmPublication: true}
	if _, e = s.moveArticle(req); e != nil {
		t.Fatal(e)
	}
	if _, e = s.submitTranslation(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); e != nil {
		t.Fatal(e)
	}
	manager.Once(context.Background())
	manager.Once(context.Background())
	a, e := s.article("program", "docs/a.md", false)
	if e != nil || a.Translation == nil || s.moves()[0].State != "source_changed" {
		t.Fatal("translation was lost", e, s.moves())
	}
}

func TestMovePrivateToPublicRequiresSeparatePublicationConsent(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	source(t, s, "public", "public")
	s.put("local", "a.md", "# Private", 0)
	req := moveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "public", DestinationPath: "a.md", ConfirmMove: true}
	if _, e := s.moveArticle(req); e != ErrInvalid {
		t.Fatal("publication without consent accepted", e)
	}
	if _, e := s.article("local", "a.md", false); e != nil {
		t.Fatal("source lost on refusal", e)
	}
	req.ConfirmPublication = true
	if m, e := s.moveArticle(req); e != nil || m.State != "complete" {
		t.Fatal("approved move failed", m, e)
	}
	if _, e := s.article("public", "a.md", true); e != nil {
		t.Fatal("approved destination not visible", e)
	}
}
