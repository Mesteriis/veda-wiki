package transfer

import (
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestMoveBetweenProgramSourcesSurvivesRestart(t *testing.T) {
	s := newFixture(t)
	s.programSource("from", "private")
	s.programSource("to", "public")
	if _, err := s.put("from", "docs/a.md", "# Original", 0); err != nil {
		t.Fatal(err)
	}
	m, err := s.Move(MoveRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != nil || m.State != "complete" {
		t.Fatal("move", m, err)
	}
	for _, public := range []bool{false, true} {
		if page, err := s.page("to/docs/b.md", public); err != nil || page.Text != "# Original" {
			t.Fatal("moved article missing", public, err)
		}
	}
	key, err := s.auth().CreateAccessKey("agent", []string{"to"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Version != domain.RevisionsFormat {
		t.Fatal("restart failed or changed the store format", err)
	}
	if len(reopened.Moves()) != 1 || reopened.Moves()[0].State != "complete" {
		t.Fatal("move lost after restart")
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

func TestCopyBetweenProgramSourcesKeepsSource(t *testing.T) {
	s := newFixture(t)
	s.programSource("from", "private")
	s.programSource("to", "public")
	if _, err := s.put("from", "docs/a.md", "# Original", 0); err != nil {
		t.Fatal(err)
	}
	_, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "docs/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/b.md", ConfirmPublication: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, public := range []bool{false, true} {
		if page, err := s.page("to/docs/b.md", public); err != nil || page.Text != "# Original" {
			t.Fatal("copied article missing", public, err)
		}
	}
	if _, err := s.article("from", "docs/a.md", false); err != nil {
		t.Fatal("copy removed source", err)
	}
}
