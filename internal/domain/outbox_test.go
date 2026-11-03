package domain

import (
	"strings"
	"testing"
)

func TestPendingDirectoryReadmeDependencySurvivesEdit(t *testing.T) {
	got := RetainedPendingLinks("[Docs](docs/)", "guide.md", map[string]string{"docs/README.md": "expected"})
	if len(got) != 1 || got["docs/README.md"] != "expected" {
		t.Fatal("directory dependency lost")
	}
	if len(RetainedPendingLinks("[Other](elsewhere/)", "guide.md", got)) != 0 {
		t.Fatal("removed directory dependency retained")
	}
}

func TestRootDependenciesRemainBoundWhenQueuedArticleChanges(t *testing.T) {
	ref := BlobRef{SHA: strings.Repeat("a", 64), MIME: "image/png"}
	if got := RetainedPendingAssets("![Icon](/img/icon.png)", "nested/a.md", map[string]BlobRef{"img/icon.png": ref}); got["img/icon.png"] != ref {
		t.Fatal("root image dependency lost")
	}
	if got := RetainedPendingLinks("[Guide](/guide.md)", "nested/a.md", map[string]string{"guide.md": "sha"}); got["guide.md"] != "sha" {
		t.Fatal("root link dependency lost")
	}
}
