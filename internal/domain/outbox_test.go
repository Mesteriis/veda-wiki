package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
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

func TestQueuedWriteCarriesDependenciesAndBundle(t *testing.T) {
	db := &Database{Sources: map[string]Source{"dst": {ID: "dst", Visibility: "private"}}, Articles: map[string]Article{}}
	a := Article{SourceID: "dst", Path: "a.md"}
	rev := Revision{Number: 1, Text: "# A", SHA: Digest([]byte("# A"))}
	ref := BlobRef{SHA: strings.Repeat("a", 64), MIME: "image/png"}
	EnqueueWrite(db, a, rev, time.Now())
	if err := AddWriteDependencies(db, a, rev, map[string]BlobRef{"img.png": ref}, map[string]string{"b.md": "sha"}); err != nil {
		t.Fatal(err)
	}
	job := db.Outbox["dst/a.md@1"]
	if job.State != "pending" || job.Assets["img.png"] != ref || job.Links["b.md"] != "sha" {
		t.Fatal("dependencies not queued", job)
	}
	other := BlobRef{SHA: strings.Repeat("b", 64), MIME: "image/png"}
	if err := AddWriteDependencies(db, a, rev, map[string]BlobRef{"img.png": other}, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting image accepted", err)
	}
	if err := AddWriteDependencies(db, a, rev, nil, map[string]string{"b.md": "changed"}); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting link accepted", err)
	}
	EnqueueBundle(db, Article{SourceID: "dst", Path: "root.md"}, rev, map[string]BlobRef{"img.png": ref}, map[string]string{"b.md": "sha"}, map[string]string{"c.md": "bundled"}, time.Now())
	root := db.Outbox["dst/root.md@1"]
	if root.State != "pending" || root.Assets["img.png"] != ref || root.Links["b.md"] != "sha" || root.Bundle["c.md"] != "bundled" {
		t.Fatal("bundle not queued", root)
	}
}
