package syncer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

func TestMoveWithLinkToConnectedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "from", "private")
	s.put("from", "guide.md", "# Shared", 0)
	s.put("from", "a.md", "[Guide](guide.md)", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "guide.md": "# Shared"})
	manager := connectFixture(t, s, f, true)
	m, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil || m.State != "waiting_destination" || len(s.jobs()[0].Links) != 1 {
		t.Fatal("link not queued", m, err)
	}
	manager.Once(context.Background())
	if s.db().Articles["from/a.md"].Deleted {
		t.Fatal("source deleted before observation")
	}
	manager.Once(context.Background())
	if s.moves()[0].State != "complete" || f.Contents()["guide.md"] != "# Shared" {
		t.Fatal("linked move incomplete", s.moves())
	}
}

func TestChangedRemoteLinkPreventsSourceDeletion(t *testing.T) {
	s, manager, original, destination := connectedMoveFixtures(t)
	original.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "[Guide](guide.md)", "guide.md": "# Shared"})
	destination.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination", "guide.md": "# Shared"})
	syncFixtureSource(t, manager, "one")
	syncFixtureSource(t, manager, "two")
	_, err := s.moveArticle(transfer.MoveRequest{SourceID: "one", SourcePath: "README.md", ExpectedRevision: 2, DestinationID: "two", DestinationPath: "moved.md", ConfirmMove: true})
	if err != nil {
		t.Fatal(err)
	}
	syncFixtureSource(t, manager, "two")
	syncFixtureSource(t, manager, "two")
	if s.moves()[0].State != "waiting_source_delete" {
		t.Fatal("target not observed", s.moves())
	}
	destination.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Destination", "moved.md": "[Guide](guide.md)", "guide.md": "# Changed"})
	syncFixtureSource(t, manager, "one")
	if original.WriteCount() != 0 || original.Contents()["README.md"] == "" {
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "guide.md": "# Shared"})
	manager := connectFixture(t, s, f, true)
	if _, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "handbook", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true}); err != nil {
		t.Fatal(err)
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Target", "guide.md": "# Changed"})
	manager.Once(context.Background())
	manager.Once(context.Background())
	if s.db().Articles["from/a.md"].Deleted || f.Contents()["b.md"] != "" || f.Contents()["guide.md"] != "# Changed" {
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
	if _, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true}); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(s.Dir(), "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, old, "--data", s.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(output), "unsupported store") {
		t.Fatal("old binary did not reject v8")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(before) != string(after) {
		t.Fatal("old binary changed v8 data")
	}
}
