package transfer

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func TestMoveLocalAtomicAndRetainsHistory(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	if _, e := s.put("from", "a.md", "# Local", 0); e != nil {
		t.Fatal(e)
	}
	req := MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md"}
	if _, e := s.Move(req); e != domain.ErrInvalid {
		t.Fatal("move without consent", e)
	}
	req.ConfirmMove = true
	m, e := s.Move(req)
	if e != nil || m.State != "complete" {
		t.Fatal(m, e)
	}
	if e := s.visible("from/a.md", false); e != domain.ErrNotFound {
		t.Fatal("source still visible", e)
	}
	target := s.db().Articles["to/a.md"]
	if e := s.visible("to/a.md", false); e != nil || target.Versions[0].Text != "# Local" || target.CopiedFrom == nil {
		t.Fatal("destination missing", e)
	}
	reopened, e := s.reopen()
	if e != nil || reopened.db().Version != 2 || !reopened.db().Articles["from/a.md"].Deleted || len(reopened.db().Articles["from/a.md"].Versions) != 1 || reopened.Moves()[0].State != "complete" {
		t.Fatal("move history not durable", e)
	}
}

func TestMovePrivateToPublicRequiresSeparatePublicationConsent(t *testing.T) {
	s := newFixture(t)
	s.source("local", "private")
	s.source("public", "public")
	s.put("local", "a.md", "# Private", 0)
	req := MoveRequest{SourceID: "local", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "public", DestinationPath: "a.md", ConfirmMove: true}
	if _, e := s.Move(req); e != domain.ErrInvalid {
		t.Fatal("publication without consent accepted", e)
	}
	if e := s.visible("local/a.md", false); e != nil {
		t.Fatal("source lost on refusal", e)
	}
	req.ConfirmPublication = true
	if m, e := s.Move(req); e != nil || m.State != "complete" {
		t.Fatal("approved move failed", m, e)
	}
	if e := s.visible("public/a.md", true); e != nil {
		t.Fatal("approved destination not visible", e)
	}
}

func TestMoveLinkedGraphToLocalIsAtomic(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "[A](a.md)", 0)
	move, err := s.Move(MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", ConfirmMove: true, IncludeLinked: true})
	if err != nil || move.State != "complete" || !s.db().Articles["from/a.md"].Deleted || s.db().Articles["from/b.md"].Deleted || s.db().Articles["to/b.md"].CopiedFrom == nil {
		t.Fatal(move, err)
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Version != 8 || len(reopened.Moves()[0].Links) != 1 {
		t.Fatal("move not durable", err)
	}
}

func TestMoveDirectoryReadmeRequiresMatchingDestinationAndPersistsSQLite(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	s.put("from", "guide.md", "# Guide\n\n[Docs](docs/)", 0)
	rev, _ := s.put("from", "docs/README.md", "# Docs", 0)
	in := MoveRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", ConfirmMove: true}
	if _, err := s.Move(in); err != domain.ErrConflict {
		t.Fatal("move accepted missing destination README", err)
	}
	s.put("to", "docs/README.md", "# Docs", 0)
	if err := store.MigrateSQLite(s.Store.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := s.reopen()
	if err != nil {
		t.Fatal(err)
	}
	move, err := s.Move(in)
	if err != nil || move.State != "complete" || move.Links["docs/README.md"] != rev.SHA || s.db().Version != 16 {
		t.Fatal("move lost directory dependency", err)
	}
	s, err = s.reopen()
	if err != nil || s.db().Version != 16 || s.Moves()[0].Links["docs/README.md"] != rev.SHA {
		t.Fatal("directory dependency lost after SQLite reopen", err)
	}
}

func TestTransferRejectsMissingOrChangedLinkedArticle(t *testing.T) {
	for _, target := range []string{"", "# Different"} {
		s := newFixture(t)
		s.source("from", "private")
		s.source("to", "private")
		s.put("from", "guide.md", "# Shared", 0)
		s.put("from", "a.md", "[Guide](guide.md)", 0)
		if target != "" {
			s.put("to", "guide.md", target, 0)
		}
		_, err := s.Move(MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true})
		if err != domain.ErrConflict || s.db().Articles["from/a.md"].Deleted {
			t.Fatal("unmatched link accepted", err)
		}
	}
}

func TestRenamedSelfLinkDoesNotPointToAnotherArticle(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	text := "[Self](a.md#part)"
	s.put("from", "a.md", text, 0)
	s.put("to", "a.md", text, 0)
	_, err := s.Move(MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true})
	if err != domain.ErrInvalid || s.db().Articles["from/a.md"].Deleted {
		t.Fatal("renamed self-link redirected", err)
	}
}

func TestPublicTargetRequiresPublishedLinkedRevision(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "public")
	s.put("from", "guide.md", "# Shared", 0)
	s.put("to", "guide.md", "# Shared", 0)
	s.put("from", "a.md", "[Guide](guide.md)", 0)
	s.mutate(func(db *domain.Database) {
		a := db.Articles["to/guide.md"]
		a.Published = nil
		db.Articles["to/guide.md"] = a
	})
	_, err := s.Move(MoveRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "b.md", ConfirmMove: true, ConfirmPublication: true})
	if err != domain.ErrConflict || s.db().Articles["from/a.md"].Deleted {
		t.Fatal("unpublished linked article accepted", err)
	}
}

func TestCancelMoveOnlyBeforeSourceDeletion(t *testing.T) {
	s := newFixture(t)
	s.mutate(func(db *domain.Database) {
		db.Moves = map[string]domain.Move{
			"waiting":  {ID: "waiting", State: "waiting_destination", Created: "2026-01-02T00:00:00Z"},
			"pending":  {ID: "pending", State: "waiting_source_delete", Created: "2026-01-01T00:00:00Z"},
			"deleting": {ID: "deleting", State: "deleting_source", Created: "2026-01-01T00:00:00Z"},
		}
	})
	before := s.state()
	if err := s.CancelMove("waiting", false); err != domain.ErrInvalid {
		t.Fatal("cancellation without confirmation", err)
	}
	if err := s.CancelMove("missing", true); err != domain.ErrNotFound {
		t.Fatal("unknown move cancelled", err)
	}
	if err := s.CancelMove("deleting", true); err != domain.ErrConflict {
		t.Fatal("move cancelled while deleting its source", err)
	}
	if s.state() != before {
		t.Fatal("rejected cancellation changed the store")
	}
	for _, id := range []string{"waiting", "pending"} {
		if err := s.CancelMove(id, true); err != nil {
			t.Fatal(id, err)
		}
	}
	var order []string
	for _, m := range s.Moves() {
		order = append(order, m.ID+":"+m.State)
	}
	if len(order) != 3 || order[0] != "deleting:deleting_source" || order[1] != "pending:cancelled" || order[2] != "waiting:cancelled" {
		t.Fatal("moves not cancelled or not ordered by creation then ID", order)
	}
	if err := s.CancelMove("waiting", true); err != domain.ErrConflict {
		t.Fatal("cancelled move cancelled again", err)
	}
}

func TestLinkContentsRequireExpectedRevisions(t *testing.T) {
	s := newFixture(t)
	s.source("to", "private")
	rev, err := s.put("to", "guide.md", "# Guide", 0)
	if err != nil {
		t.Fatal(err)
	}
	texts, err := s.LinkContents("to", map[string]string{"guide.md": rev.SHA})
	if err != nil || len(texts) != 1 || texts["guide.md"] != "# Guide" {
		t.Fatal("linked text unavailable", texts, err)
	}
	if _, err = s.LinkContents("to", map[string]string{"guide.md": domain.Digest([]byte("# Other"))}); err != domain.ErrConflict {
		t.Fatal("changed linked article accepted", err)
	}
	if _, err = s.LinkContents("to", map[string]string{"missing.md": rev.SHA}); err != domain.ErrConflict {
		t.Fatal("missing linked article accepted", err)
	}
}
