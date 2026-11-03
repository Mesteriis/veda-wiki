package importer

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestCheckoutTypesMetadataAndExclusions(t *testing.T) {
	wiki := gitFixture(t, map[string]string{"veda.yaml": metadataFixture, "AGENTS.md": "# Routing", "topics/guide.md": "# Guide", ".hidden/secret.md": "exclude", "node_modules/pkg/README.md": "exclude", "app.go": "exclude"})
	batch, e := PrepareCheckout(wiki, "handbook")
	if e != nil {
		t.Fatal(e)
	}
	if batch.Kind != "wiki" || batch.Dirty || len(batch.Files) != 2 || batch.Metadata.Title != "Handbook" {
		t.Fatalf("bad wiki observation: %+v", batch)
	}
	program := gitFixture(t, map[string]string{"README.md": "# Program", "docs/AGENTS.md": "# Context", "docs/guide.md": "# Installation", "src/main.go": "package main"})
	batch, e = PrepareCheckout(program, "program")
	if e != nil || batch.Kind != "program" || len(batch.Files) != 2 {
		t.Fatal(batch, e)
	}
	if _, ok := batch.Files["README.md"]; ok {
		t.Fatal("program root README imported")
	}
	missing := gitFixture(t, map[string]string{"README.md": "# No documentation", "do/file.md": "not docs"})
	if _, e := PrepareCheckout(missing, "program"); !errors.Is(e, domain.ErrNoDocs) {
		t.Fatal(e)
	}
}

func TestCheckoutWithholdsUnmergedDocumentation(t *testing.T) {
	for _, removeWorkingFile := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing_worktree=%t", removeWorkingFile), func(t *testing.T) {
			repo := gitFixture(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Original", "other.md": "# Other"})
			initial, err := PrepareCheckout(repo, "handbook")
			if err != nil {
				t.Fatal(err)
			}
			s := newFixture(t)
			empty := ""
			initial.Create, initial.Visibility, initial.ExpectedImport = true, "private", &empty
			if _, err := s.importBatch(initial); err != nil {
				t.Fatal(err)
			}
			gitTest(t, repo, "checkout", "-qb", "alternate")
			writeFixture(t, repo, "guide.md", "# Alternate")
			gitTest(t, repo, "commit", "-qam", "Alternate")
			gitTest(t, repo, "checkout", "-q", "-")
			writeFixture(t, repo, "guide.md", "# Main")
			gitTest(t, repo, "commit", "-qam", "Main")
			merge := exec.Command("git", "-C", repo, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "merge", "alternate")
			if err := merge.Run(); err == nil {
				t.Fatal("fixture merge did not conflict")
			}
			if out, err := exec.Command("git", "-C", repo, "ls-files", "--unmerged", "--", "guide.md").Output(); err != nil || len(out) == 0 {
				t.Fatal("fixture has no unmerged index entry", err)
			}
			if removeWorkingFile {
				if err := os.Remove(filepath.Join(repo, "guide.md")); err != nil {
					t.Fatal(err)
				}
			}
			batch, err := PrepareCheckout(repo, "handbook")
			if err != nil {
				t.Fatal(err)
			}
			if batch.Complete || batch.Withheld != 1 || batch.withheldReasons["unmerged"] != 1 {
				t.Fatal("conflict not reported as partial", batch.Complete, batch.Withheld)
			}
			if _, ok := batch.Files["guide.md"]; ok {
				t.Fatal("unmerged content was imported")
			}
			if batch.Files["other.md"] != "# Other" {
				t.Fatal("unrelated document was lost")
			}
			batch = applyAgain(s, batch)
			if _, err := s.importBatch(batch); err != nil {
				t.Fatal(err)
			}
			if page, err := s.page("handbook/guide.md"); err != nil || page.Text != "# Original" {
				t.Fatal("partial import deleted the previous document", err)
			}
		})
	}
}

func TestCheckoutRejectsUnmergedMetadata(t *testing.T) {
	repo := gitFixture(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Guide"})
	gitTest(t, repo, "checkout", "-qb", "alternate")
	writeFixture(t, repo, "veda.yaml", strings.Replace(metadataFixture, "Handbook", "Alternate", 1))
	gitTest(t, repo, "commit", "-qam", "Alternate")
	gitTest(t, repo, "checkout", "-q", "-")
	writeFixture(t, repo, "veda.yaml", strings.Replace(metadataFixture, "Handbook", "Main", 1))
	gitTest(t, repo, "commit", "-qam", "Main")
	if err := exec.Command("git", "-C", repo, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "merge", "alternate").Run(); err == nil {
		t.Fatal("fixture merge did not conflict")
	}
	if out, err := exec.Command("git", "-C", repo, "ls-files", "--unmerged", "--", "veda.yaml").Output(); err != nil || len(out) == 0 {
		t.Fatal("fixture has no unmerged metadata entry", err)
	}
	if _, err := PrepareCheckout(repo, "handbook"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("unmerged metadata accepted", err)
	}
}

func TestCheckoutSymlinksSecretsAndSpecialFiles(t *testing.T) {
	repo := gitFixture(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Safe"})
	outside := filepath.Join(t.TempDir(), "outside.md")
	if e := os.WriteFile(outside, []byte("private outside fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(repo, "linked.md")); e != nil {
		t.Fatal(e)
	}
	writeFixture(t, repo, "credentials.md", "password=fixture-for-screening")
	gitTest(t, repo, "add", "credentials.md", "linked.md")
	batch, e := PrepareCheckout(repo, "handbook")
	if e != nil {
		t.Fatal(e)
	}
	if batch.Complete || batch.Withheld != 2 || !batch.Dirty || len(batch.Files) != 1 {
		t.Fatal("unsafe paths not withheld", batch.Withheld)
	}
	writeFixture(t, repo, "pipe.md", "tracked before special-file replacement")
	gitTest(t, repo, "add", "pipe.md")
	if e := os.Remove(filepath.Join(repo, "pipe.md")); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(filepath.Join(repo, "pipe.md"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := PrepareCheckout(repo, "handbook"); e == nil {
		t.Fatal("special file accepted")
	}
}

func TestCheckoutDoesNotPublishIgnoredOrUntrackedMarkdown(t *testing.T) {
	repo := gitFixture(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Tracked", ".gitignore": "*.local.md\nprivate-notes/\n"})
	writeFixture(t, repo, "secrets.local.md", "ignored private canary")
	writeFixture(t, repo, "untracked.md", "untracked private canary")
	writeFixture(t, repo, "private-notes/notes.md", "ignored directory canary")
	batch, e := PrepareCheckout(repo, "handbook")
	if e != nil {
		t.Fatal(e)
	}
	if len(batch.Files) != 1 || batch.Files["README.md"] != "# Tracked" {
		t.Fatal("non-indexed Markdown entered import")
	}
	gitTest(t, repo, "add", "untracked.md")
	batch, e = PrepareCheckout(repo, "handbook")
	if e != nil {
		t.Fatal(e)
	}
	if len(batch.Files) != 2 || !batch.Dirty {
		t.Fatal("explicitly staged documentation not observed")
	}
}

func TestCheckoutPreviewAcceptsMoreThanFormerLimit(t *testing.T) {
	files := map[string]string{"veda.yaml": metadataFixture}
	text := strings.Repeat("a", 900<<10)
	for i := 0; i < 20; i++ {
		files[fmt.Sprintf("docs/page-%02d.md", i)] = text
	}
	checkout := gitFixture(t, files)
	batch, err := PrepareCheckout(checkout, "fixture")
	if err != nil || batch.Kind != "wiki" || len(batch.Files) != 20 || !batch.Complete {
		t.Fatal("bounded large checkout was not imported", err)
	}
}
