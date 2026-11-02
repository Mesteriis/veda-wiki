package main

import (
	"strings"
	"testing"
)

func TestTranslationPreservesRawHTML(t *testing.T) {
	s := testStore(t)
	if e := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	original := "# Guide\n\nSee <a href=\"https://example.org\">the guide</a>.\n\n<!-- keep this -->\n\n<div>Original block</div>\n"
	rev, e := s.put("program", "docs/guide.md", original, 0)
	if e != nil {
		t.Fatal(e)
	}
	in := translationRequest{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Руководство\n\nСмотрите <a href=\"https://example.org\">руководство</a>.\n\n<!-- keep this -->\n\n<div>Original block</div>\n"}
	for _, change := range [][2]string{{"https://example.org", "https://other.example"}, {"keep this", "changed"}, {"Original block", "Изменено"}} {
		broken := in
		broken.Text = strings.Replace(in.Text, change[0], change[1], 1)
		if _, e = s.submitTranslation(broken); e != ErrInvalid {
			t.Fatalf("changed HTML %q accepted: %v", change[0], e)
		}
	}
	if _, e = s.submitTranslation(in); e != nil {
		t.Fatalf("preserved HTML rejected: %v", e)
	}
}

func TestTranslationPreservesOriginalAndBecomesStale(t *testing.T) {
	s := testStore(t)
	if e := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
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
	in := translationRequest{SourceID: "program", Path: "docs/setup.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Установка\n\nВыполните `install` и [читайте](guide.md).\n\n```sh\necho hello\n```\n"}
	broken := in
	broken.Text = strings.Replace(in.Text, "echo hello", "echo goodbye", 1)
	if _, e = s.submitTranslation(broken); e != ErrInvalid {
		t.Fatal("changed code accepted", e)
	}
	broken = in
	broken.Text = strings.Replace(in.Text, "guide.md", "other.md", 1)
	if _, e = s.submitTranslation(broken); e != ErrInvalid {
		t.Fatal("changed link accepted", e)
	}
	if _, e = s.submitTranslation(in); e != nil {
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
	if _, e = s.submitTranslation(in); e != ErrConflict {
		t.Fatal("stale submission accepted", e)
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	got, e = reopened.page("program/docs/setup.md", false)
	if e != nil || got.Translated || got.TranslationState != "stale" {
		t.Fatal("stale state lost on restart", e)
	}
}
func TestTranslationPublicationRequiresReview(t *testing.T) {
	s := testStore(t)
	if e := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "public"}); e != nil {
		t.Fatal(e)
	}
	rev, e := s.put("program", "docs/readme.md", "# Original", 0)
	if e != nil {
		t.Fatal(e)
	}
	in := translationRequest{SourceID: "program", Path: "docs/readme.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}
	if _, e = s.submitTranslation(in); e != ErrInvalid {
		t.Fatal("unapproved publication", e)
	}
	in.ConfirmPublication = true
	if _, e = s.submitTranslation(in); e != nil {
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
	s := testStore(t)
	if e := s.addSource(Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	rev, _ := s.put("program", "docs/readme.md", "# Original", 0)
	in := translationRequest{SourceID: "program", Path: "docs/readme.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Приватный перевод", ConfirmPublication: true}
	if _, e := s.submitTranslation(in); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	src := s.db.Sources["program"]
	src.Visibility = "public"
	s.db.Sources[src.ID] = src
	s.mu.Unlock()
	candidates := s.translationCandidates()
	if len(candidates) != 1 || candidates[0].State != "publication_review" {
		t.Fatal("publication review missing", candidates)
	}
	page, e := s.page("program/docs/readme.md", true)
	if e != nil || !page.ProgramDocumentation || page.Translated || page.TranslationState != "unavailable" || page.Text != "# Original" {
		t.Fatal("private translation published", e)
	}
}

func TestStalePrivateDraftDoesNotLeakOnPublicPage(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "program", "public")
	rev, err := s.put("program", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.saveTranslation(translationRequest{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}, true)
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
