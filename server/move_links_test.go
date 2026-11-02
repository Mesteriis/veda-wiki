package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMovePreservesMatchingRelativeLink(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	rev, err := s.put("from", "guide.md", "# Shared guide", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("to", "guide.md", "# Shared guide", 0); err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("from", "section/a.md", "# Article\n\n[Read](../guide.md#start)", 0); err != nil {
		t.Fatal(err)
	}
	m, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "section/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "chapter/b.md", ConfirmMove: true})
	if err != nil || m.State != "complete" || m.Links["guide.md"] != rev.SHA {
		t.Fatal("linked move", m, err)
	}
	if _, err := s.createAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Version != 8 || reopened.moves()[0].Links["guide.md"] != rev.SHA {
		t.Fatal("v8 restart", err)
	}
	page, err := reopened.page("to/chapter/b.md", false)
	if err != nil {
		t.Fatal(err)
	}
	html, _, err := render(page)
	if err != nil || !strings.Contains(string(html), "to%2Fguide.md#start") {
		t.Fatal("link was not preserved", err)
	}
}

func TestMoveDirectoryReadmeRequiresMatchingDestinationAndPersistsSQLite(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "guide.md", "# Guide\n\n[Docs](docs/)", 0)
	rev, _ := s.put("from", "docs/README.md", "# Docs", 0)
	in := moveRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", ConfirmMove: true}
	if _, err := s.moveArticle(in); err != ErrConflict {
		t.Fatal("move accepted missing destination README", err)
	}
	s.put("to", "docs/README.md", "# Docs", 0)
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	move, err := s.moveArticle(in)
	if err != nil || move.State != "complete" || move.Links["docs/README.md"] != rev.SHA || s.db.Version != 16 {
		t.Fatal("move lost directory dependency", err)
	}
	s, err = openStore(s.dir)
	if err != nil || s.db.Version != 16 || s.moves()[0].Links["docs/README.md"] != rev.SHA {
		t.Fatal("directory dependency lost after SQLite reopen", err)
	}
}

func TestTransferRejectsMissingOrChangedLinkedArticle(t *testing.T) {
	for _, target := range []string{"", "# Different"} {
		s := testStore(t)
		source(t, s, "from", "private")
		source(t, s, "to", "private")
		s.put("from", "guide.md", "# Shared", 0)
		s.put("from", "a.md", "[Guide](guide.md)", 0)
		if target != "" {
			s.put("to", "guide.md", target, 0)
		}
		_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true})
		if err != ErrConflict || s.db.Articles["from/a.md"].Deleted {
			t.Fatal("unmatched link accepted", err)
		}
	}
}

func TestRenamedSelfLinkDoesNotPointToAnotherArticle(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	text := "[Self](a.md#part)"
	s.put("from", "a.md", text, 0)
	s.put("to", "a.md", text, 0)
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true})
	if err != ErrInvalid || s.db.Articles["from/a.md"].Deleted {
		t.Fatal("renamed self-link redirected", err)
	}
}

func TestPublicTargetRequiresPublishedLinkedRevision(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "public")
	s.put("from", "guide.md", "# Shared", 0)
	s.put("to", "guide.md", "# Shared", 0)
	s.put("from", "a.md", "[Guide](guide.md)", 0)
	s.mu.Lock()
	db := s.clone()
	a := db.Articles["to/guide.md"]
	a.Published = nil
	db.Articles["to/guide.md"] = a
	if err := s.commit(db); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != ErrConflict || s.db.Articles["from/a.md"].Deleted {
		t.Fatal("unpublished linked article accepted", err)
	}
}

func TestMoveWithLinkToConnectedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "guide.md", "# Shared", 0)
	s.put("from", "a.md", "[Guide](guide.md)", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "guide.md": "# Shared"})
	manager := connectFixture(t, s, f, true)
	m, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil || m.State != "waiting_destination" || len(s.jobs()[0].Links) != 1 {
		t.Fatal("link not queued", m, err)
	}
	manager.Once(context.Background())
	if s.db.Articles["from/a.md"].Deleted {
		t.Fatal("source deleted before observation")
	}
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || f.contents()["guide.md"] != "# Shared" {
		t.Fatal("linked move incomplete", s.moves())
	}
}

func TestChangedRemoteLinkPreventsSourceDeletion(t *testing.T) {
	s, manager, original, destination := connectedMoveFixtures(t)
	original.advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "[Guide](guide.md)", "guide.md": "# Shared"})
	destination.advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination", "guide.md": "# Shared"})
	syncFixtureSource(t, manager, "one")
	syncFixtureSource(t, manager, "two")
	_, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 2, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	syncFixtureSource(t, manager, "two")
	syncFixtureSource(t, manager, "two")
	if s.moves()[0].State != "waiting_source_delete" {
		t.Fatal("target not observed", s.moves())
	}
	destination.advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination", "moved.md": "[Guide](guide.md)", "guide.md": "# Changed"})
	syncFixtureSource(t, manager, "one")
	if original.writeCount() != 0 || original.contents()["README.md"] == "" {
		t.Fatal("source deleted after target link changed")
	}
}

func TestChangedRemoteLinkBeforeUploadKeepsSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	if _, err := s.put("from", "guide.md", "# Shared", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("from", "a.md", "[Guide](guide.md)", 0); err != nil {
		t.Fatal(err)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "guide.md": "# Shared"})
	manager := connectFixture(t, s, f, true)
	if _, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	f.advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "guide.md": "# Changed"})
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.db.Articles["from/a.md"].Deleted || f.contents()["b.md"] != "" || f.contents()["guide.md"] != "# Changed" {
		t.Fatal("transfer changed data after the linked article changed")
	}
}

func TestOldBinaryRejectsLinkedMoveStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V8_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "from", "private")
	source(t, s, "to", "private")
	s.put("from", "guide.md", "# Shared", 0)
	s.put("to", "guide.md", "# Shared", 0)
	s.put("from", "a.md", "[Guide](guide.md)", 0)
	if _, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true}); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(s.dir, "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(output), "unsupported store") {
		t.Fatal("old binary did not reject v8")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(before) != string(after) {
		t.Fatal("old binary changed v8 data")
	}
}
