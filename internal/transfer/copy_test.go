package transfer

import (
	"errors"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func TestCopyArticleChecksRevisionAndRejectsSecrets(t *testing.T) {
	s := newFixture(t)
	s.source("one", "private")
	s.source("two", "public")
	if _, e := s.put("one", "a.md", "# Original", 0); e != nil {
		t.Fatal(e)
	}
	req := CopyRequest{SourceID: "one", SourcePath: "a.md", ExpectedRevision: 2, DestinationID: "two", DestinationPath: "a.md", ConfirmPublication: true}
	if _, e := s.Copy(req); e != domain.ErrConflict {
		t.Fatal("stale revision accepted", e)
	}
	if _, e := s.put("one", "secret.md", "password = fixture-secret-value", 0); e != nil {
		t.Fatal(e)
	}
	secret := CopyRequest{SourceID: "one", SourcePath: "secret.md", ExpectedRevision: 1, DestinationID: "two", DestinationPath: "secret.md", ConfirmPublication: true}
	if _, e := s.Copy(secret); e != domain.ErrInvalid {
		t.Fatal("secret-like content published", e)
	}
	req.ExpectedRevision = 1
	req.DestinationPath = "../a.md"
	if _, e := s.Copy(req); e != domain.ErrInvalid {
		t.Fatal("invalid destination accepted", e)
	}
}

func TestCopyUpdateRejectsConcurrentConflictAndLinkedGraph(t *testing.T) {
	s := newFixture(t)
	s.source("a", "private")
	s.source("b", "private")
	if _, err := s.put("a", "x.md", "# Source", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("b", "x.md", "# Target", 0); err != nil {
		t.Fatal(err)
	}
	expected := 1
	req := CopyRequest{SourceID: "a", SourcePath: "x.md", ExpectedRevision: 1, DestinationID: "b", DestinationPath: "x.md", ExpectedDestinationRevision: &expected}
	if _, err := s.put("b", "x.md", "# Concurrent", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Copy(req); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("concurrent edit overwritten", err)
	}
	expected = 2
	req.IncludeLinked = true
	if _, err := s.Copy(req); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("linked update accepted without graph support", err)
	}
	if len(s.db().Articles["b/x.md"].Versions) != 2 {
		t.Fatal("failed update changed destination")
	}
}

func TestCopyUpdatePublicDestinationRequiresPublicationApproval(t *testing.T) {
	s := newFixture(t)
	s.source("private", "private")
	s.source("public", "public")
	if _, err := s.put("private", "x.md", "# Private", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("public", "x.md", "# Public", 0); err != nil {
		t.Fatal(err)
	}
	expected := 1
	req := CopyRequest{SourceID: "private", SourcePath: "x.md", ExpectedRevision: 1, DestinationID: "public", DestinationPath: "x.md", ExpectedDestinationRevision: &expected}
	if _, err := s.Copy(req); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("private update published without approval", err)
	}
	if s.db().Articles["public/x.md"].Versions[0].Text != "# Public" {
		t.Fatal("rejected update changed article")
	}
	req.ConfirmPublication = true
	if _, err := s.Copy(req); err != nil {
		t.Fatal(err)
	}
	a := s.db().Articles["public/x.md"]
	if a.Published == nil || a.Published.Text != "# Private" || len(a.Versions) != 2 {
		t.Fatal("approved update not published")
	}
}

func TestCopyUpdateSQLiteReopen(t *testing.T) {
	s := newFixture(t)
	s.source("a", "private")
	s.source("b", "private")
	if _, err := s.put("a", "x.md", "# From", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.put("b", "x.md", "# Before", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSQLite(s.Store.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	expected := 1
	if _, err = s.Copy(CopyRequest{SourceID: "a", SourcePath: "x.md", ExpectedRevision: 1, DestinationID: "b", DestinationPath: "x.md", ExpectedDestinationRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	a := reopened.db().Articles["b/x.md"]
	if len(a.Versions) != 2 || a.Versions[0].Text != "# Before" || a.CopiedFrom == nil || a.CopiedFrom.SHA != a.Versions[1].SHA {
		t.Fatal("update did not survive reopen")
	}
}
