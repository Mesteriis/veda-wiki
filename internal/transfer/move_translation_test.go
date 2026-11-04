package transfer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/translation"
)

func TestOldBinaryRejectsTranslatedMoveStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V5_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := newFixture(t)
	s.programSource("from", "private")
	s.programSource("to", "private")
	rev, _ := s.put("from", "docs/a.md", "# Original", 0)
	if _, err := s.translation().Submit(translation.Request{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Move(MoveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true}); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(s.Store.Dir(), "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, old, "--data", s.Store.Dir(), "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0")
	var exit *exec.ExitError
	output, err := command.CombinedOutput()
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(output), "unsupported store") {
		t.Fatal("old binary did not reject version 5")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(after) != string(before) {
		t.Fatal("old binary changed the store")
	}
}

func TestMovePreservesTranslationAsPrivateDraft(t *testing.T) {
	s := newFixture(t)
	s.programSource("from", "private")
	s.programSource("to", "public")
	rev, err := s.put("from", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.translation().Submit(translation.Request{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Move(MoveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil || m.State != "complete" || m.TranslationFingerprint == "" {
		t.Fatal("move", m, err)
	}
	private, err := s.page("to/docs/b.md", false)
	if err != nil || !private.Translated || private.Text != "# Перевод" {
		t.Fatal("translation not retained", err)
	}
	public, err := s.page("to/docs/b.md", true)
	if err != nil || public.Translated || public.Text != "# Original" {
		t.Fatal("translation became public", err)
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Version != domain.RevisionsFormat {
		t.Fatal("restart failed or changed the store format", err)
	}
	if reopened.Moves()[0].TranslationFingerprint != m.TranslationFingerprint {
		t.Fatal("move fingerprint lost")
	}
	if len(reopened.auth().AccessScope(key, time.Now())) != 1 {
		t.Fatal("scope lost after restart")
	}
	if err := reopened.auth().RevokeAccessKey("agent"); err != nil {
		t.Fatal(err)
	}
	reopened, err = s.reopen()
	if err != nil || reopened.db().Version != domain.RevisionsFormat || len(reopened.auth().AccessScope(key, time.Now())) != 0 {
		t.Fatal("revocation did not survive restart", err)
	}
}

func TestCopyRetainsTranslationWithoutPublishingIt(t *testing.T) {
	s := newFixture(t)
	s.programSource("from", "private")
	s.programSource("to", "public")
	rev, _ := s.put("from", "docs/a.md", "# Original", 0)
	if _, err := s.translation().Submit(translation.Request{SourceID: "from", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.page("to/docs/b.md", false)
	if err != nil || !private.Translated {
		t.Fatal("private translation lost", err)
	}
	public, err := s.page("to/docs/b.md", true)
	if err != nil || public.Translated {
		t.Fatal("translation published without review", err)
	}
	if _, err := s.article("from", "docs/a.md", false); err != nil {
		t.Fatal("copy removed source", err)
	}
}
