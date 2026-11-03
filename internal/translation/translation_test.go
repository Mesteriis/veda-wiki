package translation

import (
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func TestTranslationPreservesOriginalAndBecomesStale(t *testing.T) {
	s := newFixture(t)
	if e := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	original := "# Setup\n\nRun `install` and [read](guide.md).\n\n```sh\necho hello\n```\n"
	rev, e := s.put("program", "docs/setup.md", original, 0)
	if e != nil {
		t.Fatal(e)
	}
	initial, e := s.page("program/docs/setup.md", false)
	if e != nil || !initial.ProgramDocumentation || initial.Translated || initial.TranslationState != "unavailable" {
		t.Fatal("original program documentation is not identified", e)
	}
	in := Request{SourceID: "program", Path: "docs/setup.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Установка\n\nВыполните `install` и [читайте](guide.md).\n\n```sh\necho hello\n```\n"}
	broken := in
	broken.Text = strings.Replace(in.Text, "echo hello", "echo goodbye", 1)
	if _, e = s.Submit(broken); e != domain.ErrInvalid {
		t.Fatal("changed code accepted", e)
	}
	broken = in
	broken.Text = strings.Replace(in.Text, "guide.md", "other.md", 1)
	if _, e = s.Submit(broken); e != domain.ErrInvalid {
		t.Fatal("changed link accepted", e)
	}
	if _, e = s.Submit(in); e != nil {
		t.Fatal(e)
	}
	got, e := s.page("program/docs/setup.md", false)
	if e != nil || !got.Translated || got.TranslationState != "current" || got.OriginalSHA != rev.SHA || got.Text != in.Text {
		t.Fatal("translation not visible", e)
	}
	source, e := s.article("program", "docs/setup.md", false)
	if e != nil || source.Versions[0].Text != original {
		t.Fatal("original overwritten", e)
	}
	if _, e = s.put("program", "docs/setup.md", original+"\nChanged upstream.", 1); e != nil {
		t.Fatal(e)
	}
	got, e = s.page("program/docs/setup.md", false)
	if e != nil || !got.ProgramDocumentation || got.Translated || got.TranslationState != "stale" || !strings.Contains(got.Text, "Changed upstream") {
		t.Fatal("stale translation displayed", e)
	}
	if _, e = s.Submit(in); e != domain.ErrConflict {
		t.Fatal("stale submission accepted", e)
	}
	reopened, e := s.reopen()
	if e != nil {
		t.Fatal(e)
	}
	got, e = reopened.page("program/docs/setup.md", false)
	if e != nil || got.Translated || got.TranslationState != "stale" {
		t.Fatal("stale state lost on restart", e)
	}
}

func TestTranslationRefusesImportConflictWithoutRevisionChange(t *testing.T) {
	for _, sqlite := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sqlite"}[sqlite], func(t *testing.T) {
			s := newFixture(t)
			batch := testBatch()
			batch.Kind, batch.Metadata = "program", nil
			batch.Files = map[string]string{"docs/guide.md": "# Original"}
			if _, err := s.importBatch(batch); err != nil {
				t.Fatal(err)
			}
			local, err := s.put("handbook", "docs/guide.md", "# Local edit", 1)
			if err != nil {
				t.Fatal(err)
			}
			if sqlite {
				if err = store.MigrateSQLite(s.Dir()); err != nil {
					t.Fatal(err)
				}
				s, err = s.reopen()
				if err != nil {
					t.Fatal(err)
				}
			}
			proposal := Request{SourceID: "handbook", Path: "docs/guide.md", ExpectedRevision: 2, OriginalSHA: local.SHA, Text: "# Локальная правка"}
			if _, err = s.Validate(proposal); err != nil {
				t.Fatal("valid proposal rejected", err)
			}
			batch = applyAgain(s, batch)
			batch.Files["docs/guide.md"] = "# Remote edit"
			result, err := s.importBatch(batch)
			if err != nil || result.Conflicts != 1 {
				t.Fatal("conflict not created", err)
			}
			if _, err = s.Validate(proposal); err != domain.ErrConflict {
				t.Fatal("conflicted validation accepted", err)
			}
			if _, err = s.Submit(proposal); err != domain.ErrConflict {
				t.Fatal("conflicted submission accepted", err)
			}
			if _, err = s.SaveDraft(proposal); err != domain.ErrConflict {
				t.Fatal("conflicted automatic draft accepted", err)
			}
			if candidates := s.Candidates(); len(candidates) != 1 || candidates[0].State != "blocked_conflict" {
				t.Fatal("conflicted translation did not report its blocker", candidates)
			}
			reopened, err := s.reopen()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = reopened.Submit(proposal); err != domain.ErrConflict {
				t.Fatal("restart lost conflict", err)
			}
			if err = reopened.resolveConflict("handbook", "docs/guide.md", 0, 2, "local"); err != nil {
				t.Fatal(err)
			}
			if _, err = reopened.Submit(proposal); err != nil {
				t.Fatal("resolved translation rejected", err)
			}
		})
	}
}
func TestTranslationPublicationRequiresReview(t *testing.T) {
	s := newFixture(t)
	if e := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "public"}); e != nil {
		t.Fatal(e)
	}
	rev, e := s.put("program", "docs/readme.md", "# Original", 0)
	if e != nil {
		t.Fatal(e)
	}
	in := Request{SourceID: "program", Path: "docs/readme.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}
	if _, e = s.Submit(in); e != domain.ErrInvalid {
		t.Fatal("unapproved publication", e)
	}
	in.ConfirmPublication = true
	if _, e = s.Submit(in); e != nil {
		t.Fatal(e)
	}
	page, e := s.page("program/docs/readme.md", true)
	if e != nil || !page.Translated || page.Text != in.Text {
		t.Fatal("approved translation missing", e)
	}
	a, e := s.article("program", "docs/readme.md", true)
	if e != nil || a.Translation != nil || a.Versions[0].Text != "# Original" {
		t.Fatal("public API leaked translation metadata or changed original", e)
	}
}
func TestPrivateTranslationCannotAppearAfterVisibilityChanges(t *testing.T) {
	s := newFixture(t)
	if e := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	rev, _ := s.put("program", "docs/readme.md", "# Original", 0)
	in := Request{SourceID: "program", Path: "docs/readme.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Приватный перевод", ConfirmPublication: true}
	if _, e := s.Submit(in); e != nil {
		t.Fatal(e)
	}
	s.mutate(func(db *domain.Database) {
		src := db.Sources["program"]
		src.Visibility = "public"
		db.Sources[src.ID] = src
	})
	candidates := s.Candidates()
	if len(candidates) != 1 || candidates[0].State != "publication_review" {
		t.Fatal("publication review missing", candidates)
	}
	page, e := s.page("program/docs/readme.md", true)
	if e != nil || !page.ProgramDocumentation || page.Translated || page.TranslationState != "unavailable" || page.Text != "# Original" {
		t.Fatal("private translation published", e)
	}
}

func TestStalePrivateDraftDoesNotLeakOnPublicPage(t *testing.T) {
	s := newFixture(t)
	programSource(t, s, "program", "public")
	rev, err := s.put("program", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveDraft(Request{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("program", "docs/a.md", "# Updated", 1); err != nil {
		t.Fatal(err)
	}
	private, err := s.page("program/docs/a.md", false)
	if err != nil || private.TranslationState != "stale" || private.Translated {
		t.Fatal("private reader did not see stale translation state", err)
	}
	public, err := s.page("program/docs/a.md", true)
	if err != nil || public.TranslationState != "unavailable" || public.TranslationOrigin != "" || public.Translated {
		t.Fatal("public page exposed unpublished translation state", err)
	}
}
