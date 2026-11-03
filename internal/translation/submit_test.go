package translation

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

func TestTranslationPreservesTablesAndTaskStates(t *testing.T) {
	s := newFixture(t)
	if err := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); err != nil {
		t.Fatal(err)
	}
	original := "# Guide\n\n| Host | State |\n| :--- | ---: |\n| Alpha | Ready |\n| Beta | Waiting |\n\n- [x] Backup\n- [ ] Deploy\n"
	rev, err := s.put("program", "docs/guide.md", original, 0)
	if err != nil {
		t.Fatal(err)
	}
	translation := "# Руководство\n\n| Узел | Состояние |\n| :--- | ---: |\n| Альфа | Готов |\n| Бета | Ожидание |\n\n- [x] Резервная копия\n- [ ] Развёртывание\n"
	in := Request{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: translation}
	for name, text := range map[string]string{
		"removed table row": strings.Replace(translation, "| Бета | Ожидание |\n", "", 1),
		"removed column":    strings.Replace(translation, "| Узел | Состояние |\n| :--- | ---: |\n| Альфа | Готов |\n| Бета | Ожидание |", "| Узел |\n| :--- |\n| Альфа |\n| Бета |", 1),
		"changed alignment": strings.Replace(translation, "| :--- | ---: |", "| :--- | :--- |", 1),
		"changed checkbox":  strings.Replace(translation, "- [ ] Развёртывание", "- [x] Развёртывание", 1),
		"removed checkbox":  strings.Replace(translation, "- [ ] Развёртывание", "- Развёртывание", 1),
	} {
		t.Run(name, func(t *testing.T) {
			broken := in
			broken.Text = text
			result, err := s.Validate(broken)
			if err != nil || result["structural_valid"] != false {
				t.Fatal("invalid proposal passed structural validation", err)
			}
			if _, err := s.Submit(broken); err != domain.ErrInvalid {
				t.Fatal("invalid translation accepted", err)
			}
		})
	}
	if _, err = s.Submit(in); err != nil {
		t.Fatal("valid translation rejected", err)
	}
}

func TestTranslationPreservesRawHTML(t *testing.T) {
	s := newFixture(t)
	if e := s.addSource(domain.Source{ID: "program", Title: "Program", Kind: "program", Visibility: "private"}); e != nil {
		t.Fatal(e)
	}
	original := "# Guide\n\nSee <a href=\"https://example.org\">the guide</a>.\n\n<!-- keep this -->\n\n<div>Original block</div>\n"
	rev, e := s.put("program", "docs/guide.md", original, 0)
	if e != nil {
		t.Fatal(e)
	}
	in := Request{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Руководство\n\nСмотрите <a href=\"https://example.org\">руководство</a>.\n\n<!-- keep this -->\n\n<div>Original block</div>\n"}
	for _, change := range [][2]string{{"https://example.org", "https://other.example"}, {"keep this", "changed"}, {"Original block", "Изменено"}} {
		broken := in
		broken.Text = strings.Replace(in.Text, change[0], change[1], 1)
		if _, e = s.Submit(broken); e != domain.ErrInvalid {
			t.Fatalf("changed HTML %q accepted: %v", change[0], e)
		}
	}
	if _, e = s.Submit(in); e != nil {
		t.Fatalf("preserved HTML rejected: %v", e)
	}
}

func TestTranslationPreservesHeadingAndListStructure(t *testing.T) {
	s := newFixture(t)
	programSource(t, s, "program", "private")
	original := "# Setup\n\n## Steps\n\n1. First\n   - Nested\n2. Second\n"
	rev, err := s.put("program", "docs/guide.md", original, 0)
	if err != nil {
		t.Fatal(err)
	}
	valid := "# Установка\n\n## Шаги\n\n1. Первый\n   - Вложенный\n2. Второй\n"
	in := Request{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: valid}
	for _, broken := range []string{
		strings.Replace(valid, "## Шаги", "### Шаги", 1),
		strings.Replace(valid, "2. Второй\n", "", 1),
		strings.Replace(valid, "   - Вложенный", "- Вложенный", 1),
		strings.Replace(valid, "1. Первый", "3. Первый", 1),
	} {
		in.Text = broken
		report, err := s.Validate(in)
		if err != nil || report["structural_valid"] != false {
			t.Fatal("altered structure was not detected", err)
		}
		if _, err = s.Submit(in); err != domain.ErrInvalid {
			t.Fatal("altered structure was stored", err)
		}
	}
	in.Text = valid
	report, err := s.Validate(in)
	if err != nil || report["structural_valid"] != true {
		t.Fatal("valid structure rejected", err)
	}
	if _, err = s.Submit(in); err != nil {
		t.Fatal("valid translation not stored", err)
	}
}

func TestOldBinaryRejectsTranslationOriginStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V11_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := newFixture(t)
	programSource(t, s, "program", "private")
	rev, err := s.put("program", "docs/a.md", "# Original", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveDraft(Request{SourceID: "program", Path: "docs/a.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"})
	if err != nil {
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
		t.Fatal("old binary did not reject version 11")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(after) != string(before) {
		t.Fatal("old binary changed state")
	}
}

func TestTranslationFingerprintPreventsConcurrentOverwrite(t *testing.T) {
	for _, sqlite := range []bool{false, true} {
		name := "json"
		if sqlite {
			name = "sqlite"
		}
		t.Run(name, func(t *testing.T) {
			s := newFixture(t)
			programSource(t, s, "program", "public")
			rev, err := s.put("program", "docs/guide.md", "# Guide", 0)
			if err != nil {
				t.Fatal(err)
			}
			if sqlite {
				if err := store.MigrateSQLite(s.Dir()); err != nil {
					t.Fatal(err)
				}
				s, err = reopenFixture(s.Dir())
				if err != nil {
					t.Fatal(err)
				}
			}
			empty := ""
			base := Request{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод", ConfirmPublication: true, ExpectedTranslationFingerprint: &empty}
			snapshot, err := s.Validate(base)
			if err != nil || snapshot["translation_fingerprint"] != "" {
				t.Fatal("incorrect empty fingerprint", err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for _, text := range []string{"# Первый перевод", "# Второй перевод"} {
				wg.Add(1)
				go func(text string) {
					defer wg.Done()
					<-start
					in := base
					in.Text = text
					_, e := s.Submit(in)
					results <- e
				}(text)
			}
			close(start)
			wg.Wait()
			close(results)
			successes, conflicts := 0, 0
			for e := range results {
				if e == nil {
					successes++
				} else if errors.Is(e, domain.ErrConflict) {
					conflicts++
				} else {
					t.Fatal(e)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatal("concurrent writes were not serialized", successes, conflicts)
			}
			if _, err := s.Validate(base); !errors.Is(err, domain.ErrConflict) {
				t.Fatal("stale validation accepted", err)
			}
			base.ExpectedTranslationFingerprint = nil
			snapshot, err = s.Validate(base)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, ok := snapshot["translation_fingerprint"].(string)
			if !ok || len(fingerprint) != 64 {
				t.Fatal("missing current fingerprint")
			}
			base.ExpectedTranslationFingerprint = &fingerprint
			if _, err = s.Submit(base); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Submit(base); !errors.Is(err, domain.ErrConflict) {
				t.Fatal("reused fingerprint accepted", err)
			}
			reopened, err := reopenFixture(s.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = reopened.Validate(base); !errors.Is(err, domain.ErrConflict) {
				t.Fatal("stale fingerprint accepted after reopen", err)
			}
			invalid := "not-a-fingerprint"
			base.ExpectedTranslationFingerprint = &invalid
			if _, err = reopened.Submit(base); !errors.Is(err, domain.ErrInvalid) {
				t.Fatal("invalid fingerprint accepted", err)
			}
		})
	}
}

func TestTranslationFingerprintTracksApprovalEvenWhenTextIsUnchanged(t *testing.T) {
	s := newFixture(t)
	programSource(t, s, "program", "public")
	rev, err := s.put("program", "docs/guide.md", "# Guide", 0)
	if err != nil {
		t.Fatal(err)
	}
	in := Request{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}
	draft, err := s.SaveDraft(in)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := domain.TranslationFingerprint(&draft)
	in.ConfirmPublication = true
	in.ExpectedTranslationFingerprint = &fingerprint
	if _, err = s.Submit(in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(in); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("approval change was not detected", err)
	}
}
