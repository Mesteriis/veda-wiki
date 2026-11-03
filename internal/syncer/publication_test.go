package syncer

import (
	"context"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func TestStaleOutboxObservationCannotUndoPublicationApproval(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	connectFixture(t, s, f, false)
	if _, err := s.put("handbook", "new.md", "# New", 0); err != nil {
		t.Fatal(err)
	}
	c := s.connections()[0]
	key := "handbook/new.md@1"
	s.mutate(func(db *domain.Database) {
		j := db.Outbox[key]
		j.Visibility = "private"
		db.Outbox[key] = j
	})
	old := s.jobs()[0]
	s.mutate(func(db *domain.Database) {
		j := db.Outbox[key]
		j.Visibility = "public"
		db.Outbox[key] = j
	})
	if err := s.setJobFor(c, old, "blocked", "publication_confirmation_required", ""); err != domain.ErrConflict {
		t.Fatal("changed visibility was overwritten", err)
	}
	if s.jobs()[0].State != "pending" {
		t.Fatal("changed job was blocked")
	}
	old = s.jobs()[0]
	s.mutate(func(db *domain.Database) {
		j := db.Outbox[key]
		j.State = "conflict"
		db.Outbox[key] = j
	})
	if err := s.setJobFor(c, old, "retry", "upstream_unavailable", ""); err != domain.ErrConflict {
		t.Fatal("conflict state was overwritten", err)
	}
}

func TestGitHubPrivateQueuedWritesRequirePublicationConsent(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Private unsent canary", 1)
	f.Modify(func() { f.Private = false })
	m.Once(context.Background())
	jobs := s.jobs()
	if f.WriteCount() != 0 || len(jobs) != 1 || jobs[0].LastError != "publication_confirmation_required" {
		t.Fatal("private work auto-published", jobs)
	}
	public, e := s.article("handbook", "README.md", true)
	if e != nil || public.Versions[0].Text != "# Upstream" || public.Versions[0].Number != 0 {
		t.Fatal("public history contains private metadata", public, e)
	}
	if e = s.publishJob(jobs[0].ID, 2, false); e != domain.ErrInvalid {
		t.Fatal("implicit publication accepted")
	}
	if e = s.publishJob(jobs[0].ID, 1, true); e != domain.ErrConflict {
		t.Fatal("stale publication accepted")
	}
	if e = s.connectionStatusFor(s.connections()[0], "syncing", "verified", "", nil, time.Time{}); e != nil {
		t.Fatal(e)
	}
	if e = s.publishJob(jobs[0].ID, 2, true); e != nil {
		t.Fatal(e)
	}
	// Approval publishes the reviewed revision exactly as it does between syncs;
	// a sync in progress neither hides it nor resets the connection state.
	if page, e := s.page("handbook/README.md", true); e != nil || page.Text != "# Private unsent canary" || s.connections()[0].State != "syncing" {
		t.Fatal("approved publication not served consistently during sync", page.Text, e)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.WriteCount() != 1 || s.jobs()[0].State != "complete" {
		t.Fatal(s.jobs())
	}
}

func TestGitHubExpiredPublicProofDoesNotApproveQueuedWrite(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	m := connectFixture(t, s, f, true)
	s.mutate(func(db *domain.Database) {
		c := db.Connections["handbook"]
		c.VerifiedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
		db.Connections[c.ID] = c
	})
	if _, e := s.put("handbook", "README.md", "# Unverified edit", 1); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	jobs := s.jobs()
	if f.WriteCount() != 0 || len(jobs) != 1 || jobs[0].LastError != "publication_confirmation_required" {
		t.Fatal("expired proof allowed publication", jobs)
	}
}
