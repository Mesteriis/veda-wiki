package transfer

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func TestCopyLinkedLocalCyclesAndAssets(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	ref := s.asset("from", "img/icon.png")
	s.put("from", "a.md", "[B](b.md) ![icon](img/icon.png)", 0)
	s.put("from", "b.md", "[A](a.md)", 0)
	_, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.md", "b.md"} {
		if s.db().Articles["from/"+p].Deleted || s.db().Articles["to/"+p].CopiedFrom == nil {
			t.Fatal("copy or source missing", p)
		}
	}
	if s.db().Sources["to"].Assets["img/icon.png"] != ref {
		t.Fatal("asset missing")
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Articles["to/b.md"].Versions[0].Text != "[A](a.md)" {
		t.Fatal("batch not persisted", err)
	}
}
func TestCopyLinkedLocalRejectsConflictWithoutPartialWrites(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# expected", 0)
	s.put("to", "b.md", "# different", 0)
	before := s.state()
	_, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true})
	after := s.state()
	if err != domain.ErrConflict || before != after {
		t.Fatal("conflict changed store", err)
	}
}

func TestTransferIndexDirectoryDoesNotShadowExistingDestination(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "linked"}[linked], func(t *testing.T) {
			s := newFixture(t)
			s.source("from", "private")
			s.source("to", "private")
			if _, err := s.put("from", "guide.md", "# Guide\n\n[Docs](docs/)", 0); err != nil {
				t.Fatal(err)
			}
			if _, err := s.put("from", "docs/index.md", "# Docs", 0); err != nil {
				t.Fatal(err)
			}
			if _, err := s.put("to", "docs/index.md", "# Docs", 0); err != nil {
				t.Fatal(err)
			}
			before := s.state()
			_, err := s.PlanTransfer(CopyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md"})
			if err != domain.ErrConflict {
				t.Fatal("directory link transfer plan was accepted", err)
			}
			_, err = s.Copy(CopyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", IncludeLinked: linked})
			after := s.state()
			want := domain.ErrConflict
			if err != want || before != after {
				t.Fatal("directory link copy did not fail atomically", err)
			}
			_, err = s.Move(MoveRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", IncludeLinked: linked, ConfirmMove: true})
			after = s.state()
			if err != want || before != after {
				t.Fatal("directory link move did not fail atomically", err)
			}
		})
	}
}

func TestCopyLinkedDirectoryReadmeAndRootLinks(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	for p, text := range map[string]string{
		"README.md":      "# Root",
		"docs/README.md": "# Docs\n\n[Root](../)",
		"guide.md":       "# Guide\n\n[Docs](docs/)",
	} {
		if _, err := s.put("from", p, text, 0); err != nil {
			t.Fatal(err)
		}
	}
	in := CopyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", IncludeLinked: true}
	plan, err := s.PlanTransfer(in)
	if err != nil || len(plan.Articles) != 3 {
		t.Fatal("directory graph not planned", err)
	}
	if _, err := s.Copy(in); err != nil {
		t.Fatal("directory graph not copied", err)
	}
	if s.db().Version != 16 {
		t.Fatal("directory transfer did not gate old binaries")
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Articles["to/docs/README.md"].Versions[0].Text != "# Docs\n\n[Root](../)" {
		t.Fatal("directory graph lost on reopen", err)
	}
}

func TestCopyLinkedIndexDirectoryToReadme(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	s.put("from", "guide.md", "# Guide\n\n[Docs](docs/)", 0)
	s.put("from", "docs/index.md", "# Documentation", 0)
	in := CopyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md", IncludeLinked: true}
	plan, err := s.PlanTransfer(in)
	if err != nil || len(plan.Articles) != 2 || plan.Articles[0].SourcePath != "docs/index.md" || plan.Articles[0].DestinationPath != "docs/README.md" || !plan.Articles[0].DirectoryIndex {
		t.Fatal("index mapping not exposed in plan", err)
	}
	if _, err = s.Copy(in); err != nil {
		t.Fatal("index directory copy failed", err)
	}
	a := s.db().Articles["to/docs/README.md"]
	if s.db().Version != 17 || a.CopiedFrom == nil || !a.CopiedFrom.DirectoryIndex || a.CopiedFrom.Path != "docs/index.md" {
		t.Fatal("index mapping provenance missing")
	}
	if _, err = s.reopen(); err != nil {
		t.Fatal("v17 store did not reopen", err)
	}
	if err = store.MigrateSQLite(s.Store.Dir()); err != nil {
		t.Fatal("v17 SQLite migration failed", err)
	}
	reopened, err := s.reopen()
	if err != nil || reopened.db().Version != 17 || !reopened.db().Articles["to/docs/README.md"].CopiedFrom.DirectoryIndex {
		t.Fatal("v17 provenance lost after SQLite migration", err)
	}
}

func TestIndexDirectoryAmbiguousMappingIsRejected(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	s.put("from", "guide.md", "[Directory](docs/) [File](docs/index.md)", 0)
	s.put("from", "docs/index.md", "# Documentation", 0)
	if _, err := s.PlanTransfer(CopyRequest{SourceID: "from", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "guide.md"}); err != domain.ErrConflict {
		t.Fatal("ambiguous index mapping accepted", err)
	}
}
func TestCopyLinkedLocalChecksPublicationForEveryArticle(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "public")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "# B", 0)
	in := CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true}
	if _, err := s.Copy(in); err != domain.ErrInvalid {
		t.Fatal("unapproved publication", err)
	}
	if _, ok := s.db().Articles["to/a.md"]; ok {
		t.Fatal("partial publication")
	}
	in.ConfirmPublication = true
	if _, err := s.Copy(in); err != nil {
		t.Fatal(err)
	}
	if s.db().Articles["to/b.md"].Published == nil {
		t.Fatal("linked article not published")
	}
}

func TestCopyLinkedLocalMissingImageLeavesNoPartialBatch(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "![missing](missing.png)", 0)
	before := s.state()
	_, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true})
	after := s.state()
	if err != domain.ErrInvalid || before != after {
		t.Fatal("missing image changed store", err)
	}
}

func TestCopyLinkedLocalRejectsSensitiveLinkedText(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "public")
	s.put("from", "a.md", "[B](b.md)", 0)
	s.put("from", "b.md", "password=fixture-value-only", 0)
	before := s.state()
	_, err := s.Copy(CopyRequest{SourceID: "from", SourcePath: "a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "a.md", IncludeLinked: true, ConfirmPublication: true})
	after := s.state()
	if err != domain.ErrInvalid || before != after {
		t.Fatal("sensitive linked article was published", err)
	}
}

func TestRootAssetConflictRejectsEntireCopy(t *testing.T) {
	s := newFixture(t)
	s.source("from", "private")
	s.source("to", "private")
	s.asset("from", "img/icon.png")
	other, err := domain.ImageRef(append(smallPNG(t), 1))
	if err != nil {
		t.Fatal(err)
	}
	s.mutate(func(db *domain.Database) {
		target := db.Sources["to"]
		target.Assets = map[string]domain.BlobRef{"img/icon.png": other}
		db.Sources["to"] = target
	})
	if _, err = s.put("from", "chapter/a.md", "![Icon](/img/icon.png)", 0); err != nil {
		t.Fatal(err)
	}
	before := s.state()
	_, err = s.Copy(CopyRequest{SourceID: "from", SourcePath: "chapter/a.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "other/b.md"})
	after := s.state()
	if err != domain.ErrConflict || before != after {
		t.Fatal("conflicting root asset changed the store", err)
	}
}
