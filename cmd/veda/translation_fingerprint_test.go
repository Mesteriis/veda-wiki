package main

import (
	"errors"
	"sync"
	"testing"
)

func TestTranslationFingerprintPreventsConcurrentOverwrite(t *testing.T) {
	for _, sqlite := range []bool{false, true} {
		name := "json"
		if sqlite {
			name = "sqlite"
		}
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			programSource(t, s, "program", "public")
			rev, err := s.put("program", "docs/guide.md", "# Guide", 0)
			if err != nil {
				t.Fatal(err)
			}
			if sqlite {
				if err := migrateSQLite(s.Dir()); err != nil {
					t.Fatal(err)
				}
				s, err = openStore(s.Dir())
				if err != nil {
					t.Fatal(err)
				}
			}
			empty := ""
			base := translationRequest{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод", ConfirmPublication: true, ExpectedTranslationFingerprint: &empty}
			snapshot, err := s.validateTranslation(base)
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
					_, e := s.submitTranslation(in)
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
				} else if errors.Is(e, ErrConflict) {
					conflicts++
				} else {
					t.Fatal(e)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatal("concurrent writes were not serialized", successes, conflicts)
			}
			if _, err := s.validateTranslation(base); !errors.Is(err, ErrConflict) {
				t.Fatal("stale validation accepted", err)
			}
			base.ExpectedTranslationFingerprint = nil
			snapshot, err = s.validateTranslation(base)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint, ok := snapshot["translation_fingerprint"].(string)
			if !ok || len(fingerprint) != 64 {
				t.Fatal("missing current fingerprint")
			}
			base.ExpectedTranslationFingerprint = &fingerprint
			if _, err = s.submitTranslation(base); err != nil {
				t.Fatal(err)
			}
			if _, err = s.submitTranslation(base); !errors.Is(err, ErrConflict) {
				t.Fatal("reused fingerprint accepted", err)
			}
			reopened, err := openStore(s.Dir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = reopened.validateTranslation(base); !errors.Is(err, ErrConflict) {
				t.Fatal("stale fingerprint accepted after reopen", err)
			}
			invalid := "not-a-fingerprint"
			base.ExpectedTranslationFingerprint = &invalid
			if _, err = reopened.submitTranslation(base); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid fingerprint accepted", err)
			}
		})
	}
}

func TestTranslationFingerprintTracksApprovalEvenWhenTextIsUnchanged(t *testing.T) {
	s := testStore(t)
	programSource(t, s, "program", "public")
	rev, err := s.put("program", "docs/guide.md", "# Guide", 0)
	if err != nil {
		t.Fatal(err)
	}
	in := translationRequest{SourceID: "program", Path: "docs/guide.md", ExpectedRevision: 1, OriginalSHA: rev.SHA, Text: "# Перевод"}
	draft, err := s.saveTranslation(in, true)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := translationFingerprint(&draft)
	in.ConfirmPublication = true
	in.ExpectedTranslationFingerprint = &fingerprint
	if _, err = s.submitTranslation(in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.submitTranslation(in); !errors.Is(err, ErrConflict) {
		t.Fatal("approval change was not detected", err)
	}
}
