package syncer

import (
	"context"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

// renameFixture connects a private fixture, then renames it on the fake
// GitHub so the old name answers with a redirect.
func renameFixture(t *testing.T, s *fixtureStore) (*githubtest.Fake, *Manager) {
	t.Helper()
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Before"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	if c := s.connections()[0]; c.State != "ready" || c.Repository != "org/handbook" {
		t.Fatalf("fixture not connected: %+v", c)
	}
	f.Modify(func() { f.Renamed, f.Name = f.Name, "org/renamed" })
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "guide.md": "# After"})
	return f, m
}

func TestSyncFollowsRenamedRepository(t *testing.T) {
	s := testStore(t)
	_, m := renameFixture(t, s)
	before := s.connections()[0].ConfigRevision
	m.Once(context.Background())
	c := s.connections()[0]
	if c.Repository != "org/renamed" || c.RepositoryID != githubtest.RepoID {
		t.Fatalf("connection did not follow the rename: %+v", c)
	}
	if c.State != "ready" || c.LastError != "" || c.ConfigRevision != before+1 {
		t.Fatalf("rename left the connection unhealthy: %+v", c)
	}
	if p, err := s.page("handbook/guide.md", false); err != nil || p.Text != "# After" {
		t.Fatalf("content after the rename was not imported: %v %q", err, p.Text)
	}
}

func TestSyncKeepsErrorWhenRenamedRepositoryDiffers(t *testing.T) {
	s := testStore(t)
	f, m := renameFixture(t, s)
	f.Modify(func() { f.RepoID = githubtest.RepoID + 1 })
	m.Once(context.Background())
	c := s.connections()[0]
	if c.Repository != "org/handbook" || c.State != "error" || c.LastError != "repository_moved" {
		t.Fatalf("a repository with another id must not be adopted: %+v", c)
	}
	if p, err := s.page("handbook/guide.md", false); err != nil || p.Text != "# Before" {
		t.Fatalf("content changed although the rename was rejected: %v %q", err, p.Text)
	}
}

func TestConfigureSetsRepositoryName(t *testing.T) {
	s := testStore(t)
	_, m := renameFixture(t, s)
	c := s.connections()[0]
	name := "https://github.com/org/renamed"
	updated, err := m.Configure(context.Background(), ConfigureRequest{ID: c.ID, Repository: &name, Expected: &c.ConfigRevision})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Repository != "org/renamed" || updated.ConfigRevision != c.ConfigRevision+1 || updated.State != "pending" {
		t.Fatalf("repository name not applied: %+v", updated)
	}
	m.Once(context.Background())
	if c := s.connections()[0]; c.State != "ready" || c.LastError != "" {
		t.Fatalf("sync after the explicit rename failed: %+v", c)
	}
	if p, err := s.page("handbook/guide.md", false); err != nil || p.Text != "# After" {
		t.Fatalf("content after the rename was not imported: %v %q", err, p.Text)
	}
}

func TestConfigureRejectsRepositoryWithOtherID(t *testing.T) {
	s := testStore(t)
	f, m := renameFixture(t, s)
	f.Modify(func() { f.RepoID = githubtest.RepoID + 1 })
	c := s.connections()[0]
	name := "org/renamed"
	if _, err := m.Configure(context.Background(), ConfigureRequest{ID: c.ID, Repository: &name, Expected: &c.ConfigRevision}); err != domain.ErrConflict {
		t.Fatalf("another repository must be rejected, got %v", err)
	}
	bad := "not a repository"
	if _, err := m.Configure(context.Background(), ConfigureRequest{ID: c.ID, Repository: &bad, Expected: &c.ConfigRevision}); err != domain.ErrInvalid {
		t.Fatalf("an invalid name must be rejected, got %v", err)
	}
	if c := s.connections()[0]; c.Repository != "org/handbook" || c.ConfigRevision != 1 {
		t.Fatalf("rejected configure changed the connection: %+v", c)
	}
}

func TestSyncDoesNotFollowTransferToAnotherOwner(t *testing.T) {
	s := testStore(t)
	f, m := renameFixture(t, s)
	f.Modify(func() { f.Name = "collaborator/handbook" })
	m.Once(context.Background())
	c := s.connections()[0]
	if c.Repository != "org/handbook" || c.State != "error" || c.LastError != "repository_moved" {
		t.Fatalf("a transfer to another owner must wait for the owner: %+v", c)
	}
	name := "collaborator/handbook"
	updated, err := m.Configure(context.Background(), ConfigureRequest{ID: c.ID, Repository: &name, Expected: &c.ConfigRevision})
	if err != nil || updated.Repository != name {
		t.Fatalf("explicit configuration must accept the transferred repository: %v %+v", err, updated)
	}
}

func TestSyncFollowsRenamedPublicRepositoryAnonymously(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Before"})
	m := connectFixture(t, s, f, false)
	f.Modify(func() { f.Renamed, f.Name = f.Name, "org/renamed" })
	f.Advance(map[string]string{"veda.yaml": metadataFixture, "guide.md": "# After"})
	m.Once(context.Background())
	c := s.connections()[0]
	if c.Repository != "org/renamed" || c.AccountID != "" || c.State != "ready" || c.LastError != "" {
		t.Fatalf("anonymous public connection did not follow the rename: %+v", c)
	}
	if p, err := s.page("handbook/guide.md", true); err != nil || p.Text != "# After" {
		t.Fatalf("public content after the rename was not imported: %v %q", err, p.Text)
	}
}
