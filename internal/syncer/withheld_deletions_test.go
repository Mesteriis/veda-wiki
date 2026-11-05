package syncer

import (
	"context"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func TestWithheldFileDoesNotFreezeUpstreamDeletions(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Readme", "guide.md": "# Guide", "notes.md": "# Notes"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	if len(s.snapshot(false).Pages) != 3 {
		t.Fatal("initial import incomplete")
	}
	// Upstream deleted guide.md and made notes.md credential-like.
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Readme", "notes.md": credentialFixture})
	m.Once(context.Background())
	if _, err := s.page("handbook/guide.md", false); err == nil {
		t.Fatal("upstream deletion frozen by a withheld file")
	}
	if notes, err := s.page("handbook/notes.md", false); err != nil || notes.Text != "# Notes" {
		t.Fatal("withheld article lost its previous revision", err)
	}
	if !s.db().Sources["handbook"].ImportPartial {
		t.Fatal("withheld file did not keep the source partial")
	}
	if n, ok := s.observations.Withheld("handbook"); !ok || n != 1 {
		t.Fatal("withheld count not observed", n, ok)
	}
	// A locally changed article deleted upstream still becomes a conflict.
	if _, err := s.put("handbook", "README.md", "# Local edit", 1); err != nil {
		t.Fatal(err)
	}
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "notes.md": credentialFixture})
	m.Once(context.Background())
	readme := s.db().Articles["handbook/README.md"]
	if readme.Deleted || len(readme.Conflicts) != 1 || !readme.Conflicts[0].Deleted || domain.LastText(readme) != "# Local edit" {
		t.Fatal("deletion of a locally changed article did not conflict")
	}
	if _, err := s.page("handbook/notes.md", false); err != nil {
		t.Fatal("withheld article tombstoned", err)
	}
	// Rewording the file upstream completes the source again. The blocked
	// local write put the connection on a retry delay; schedule it now.
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "notes.md": "# Notes reworded"})
	if err := s.retryConnection("handbook"); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if s.db().Sources["handbook"].ImportPartial {
		t.Fatal("source stayed partial after the withheld file was reworded")
	}
	if notes, err := s.page("handbook/notes.md", false); err != nil || notes.Text != "# Notes reworded" {
		t.Fatal("reworded article not imported", err)
	}
}

func TestRemoteBatchNamesWithheldPaths(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Readme", "secret.md": credentialFixture, "image.png": "not an image"})
	m := newSyncManager(s, f.Client())
	var head string
	f.Modify(func() { head = f.Head })
	tree, err := m.github.Tree(context.Background(), "org/handbook", head, "")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := m.remoteBatch(context.Background(), domain.GitHubConnection{ID: "handbook", Repository: "org/handbook"}, head, "", tree)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Complete || batch.Withheld != 2 || len(batch.Files) != 1 || !batch.WithheldPaths["secret.md"] || !batch.WithheldPaths["image.png"] || !batch.ObservedTree() {
		t.Fatal("withheld paths not named", batch.Withheld, batch.WithheldPaths)
	}
}
