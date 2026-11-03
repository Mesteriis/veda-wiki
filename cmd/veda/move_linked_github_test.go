package main

import (
	"context"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMoveLinkedGraphToGitHubWaitsForFullObservation(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	fixtureAsset(t, s, "from", "img/icon.png")
	s.put("from", "a.md", "[B](b.md) ![icon](img/icon.png)", 0)
	s.put("from", "b.md", "[A](a.md)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	move, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", ConfirmMove: true, ConfirmPublication: true, IncludeLinked: true})
	if err != nil || move.State != "waiting_destination" {
		t.Fatal(move, err)
	}
	m.Once(context.Background())
	if s.DB().Articles["from/a.md"].Deleted || f.Contents()["a.md"] == "" || f.Contents()["b.md"] == "" || f.Contents()["img/icon.png"] == "" {
		t.Fatal("invalid pre-observation state")
	}
	m.Once(context.Background())
	if s.moves()[0].State != "complete" || !s.DB().Articles["from/a.md"].Deleted || s.DB().Articles["from/b.md"].Deleted {
		t.Fatal("incorrect completed move", s.moves())
	}
}

func TestOldBinaryRejectsConnectedGraphMoveStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V10_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	if _, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", ConfirmMove: true, ConfirmPublication: true, IncludeLinked: true}); err != nil {
		t.Fatal(err)
	}
	if s.DB().Version != 10 {
		t.Fatal("graph move did not upgrade store")
	}
	state := filepath.Join(s.Dir(), "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, old, "--data", s.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(out), "unsupported store") {
		t.Fatal("old binary did not reject v10")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(before) != string(after) {
		t.Fatal("old binary changed v10 state")
	}
}
func TestCancelledLinkedBundleKeepsSource(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	connectFixture(t, s, f, true)
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", ConfirmMove: true, ConfirmPublication: true, IncludeLinked: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.cancelBundle(s.jobs()[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if s.moves()[0].State != "destination_changed" || s.DB().Articles["from/a.md"].Deleted {
		t.Fatal("cancelled move removed source", s.moves())
	}
}
func TestConnectedLinkedGraphRechecksRemoteBeforeDeleting(t *testing.T) {
	s, m, one, two := connectedMoveFixtures(t)
	one.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	syncFixtureSource(t, m, "one")
	move, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 2, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "waiting_destination" {
		t.Fatal(move, err)
	}
	syncFixtureSource(t, m, "two")
	syncFixtureSource(t, m, "two")
	if s.moves()[0].State != "waiting_source_delete" {
		t.Fatal("target not confirmed", s.moves())
	}
	two.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination", "moved.md": "[B](b.md)", "b.md": "# Changed"})
	syncFixtureSource(t, m, "one")
	if one.WriteCount() != 0 || one.Contents()["README.md"] == "" {
		t.Fatal("source removed after target changed")
	}
}

func TestConnectedLinkedGraphCompletesAcrossRepositories(t *testing.T) {
	s, m, one, two := connectedMoveFixtures(t)
	one.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "[B](b.md)", "b.md": "# B"})
	syncFixtureSource(t, m, "one")
	_, err := s.moveArticle(moveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 2, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil {
		t.Fatal(err)
	}
	syncFixtureSource(t, m, "two")
	if one.Contents()["README.md"] == "" || s.moves()[0].State != "waiting_destination" {
		t.Fatal("source removed before observation")
	}
	syncFixtureSource(t, m, "two")
	syncFixtureSource(t, m, "one")
	syncFixtureSource(t, m, "one")
	if s.moves()[0].State != "complete" || one.Contents()["README.md"] != "" || one.Contents()["b.md"] != "# B" || two.Contents()["b.md"] != "# B" {
		t.Fatal("move did not complete safely", s.moves())
	}
}

func TestLinkedMoveRecoversLostDestinationResponse(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target"})
	m := connectFixture(t, s, f, true)
	_, err := s.moveArticle(moveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "a.md", ConfirmMove: true, ConfirmPublication: true, IncludeLinked: true})
	if err != nil {
		t.Fatal(err)
	}
	f.Modify(func() { f.LoseResponse = true })
	m.Once(context.Background())
	if s.DB().Articles["from/a.md"].Deleted || f.WriteCount() != 1 {
		t.Fatal("source removed before confirmation")
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	newSyncManager(reopened, f.Client()).Once(context.Background())
	if reopened.moves()[0].State != "complete" || f.WriteCount() != 1 {
		t.Fatal("lost response not recovered", reopened.moves())
	}
}
