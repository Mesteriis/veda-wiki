package syncer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func TestGitHubPrivateVisibilityAndUnpublishedEdits(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, false)
	s.put("handbook", "README.md", "# Private pending canary", 1)
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("private source disclosed")
	}
	f.Modify(func() { f.Private = false })
	s.retryConnection("handbook")
	m.Once(context.Background())
	p, e := s.page("handbook/README.md", true)
	if e != nil || p.Text != "# Upstream" {
		t.Fatal("private pending edit published", p, e)
	}
	owner, _ := s.page("handbook/README.md", false)
	if owner.Text != "# Private pending canary" {
		t.Fatal("local edit lost")
	}
	f.Modify(func() { f.Private = true })
	s.retryConnection("handbook")
	m.Once(context.Background())
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("repository downgrade did not revoke public access")
	}
}

func TestGitHubMultipleRepositoriesStayIndependent(t *testing.T) {
	s := testStore(t)
	one := githubtest.New(t, map[string]string{"docs/guide.md": "# Public one"})
	two := githubtest.New(t, map[string]string{"docs/guide.md": "# Private two"})
	two.Modify(func() { two.Name = "org/second"; two.RepoID = 42; two.Private = true })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			two.Serve(w, r)
		} else {
			one.Serve(w, r)
		}
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	m := newSyncManager(s, client)
	s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access})
	if _, e := m.Connect(context.Background(), ConnectRequest{ID: "one", Repository: "org/handbook"}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Connect(context.Background(), ConnectRequest{ID: "two", Repository: "org/second"}); e == nil {
		t.Fatal("connector guessed a saved credential")
	}
	if _, e := m.Connect(context.Background(), ConnectRequest{ID: "two", Repository: "org/second", AccountID: "operator"}); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	if len(s.snapshot(false).Pages) != 2 || len(s.snapshot(true).Pages) != 1 {
		t.Fatal("merged visibility failure")
	}
	p, _ := s.page("one/docs/guide.md", true)
	if p.Text != "# Public one" {
		t.Fatal(p)
	}
	if _, e := s.page("two/docs/guide.md", true); e != domain.ErrNotFound {
		t.Fatal("private repository disclosed")
	}
}

func TestGitHubPauseAndExpiredProofFailClosed(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"docs/guide.md": "# Public"})
	m := connectFixture(t, s, f, false)
	if e := s.pauseConnection("handbook", true); e != nil {
		t.Fatal(e)
	}
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("paused connection public")
	}
	m.Once(context.Background())
	s.pauseConnection("handbook", false)
	m.Once(context.Background())
	if len(s.snapshot(true).Pages) != 1 {
		t.Fatal("resumed source not verified")
	}
	s.mutate(func(db *domain.Database) {
		c := db.Connections["handbook"]
		c.VerifiedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
		db.Connections[c.ID] = c
	})
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("expired verification published")
	}
	if len(s.snapshot(false).Pages) != 1 {
		t.Fatal("owner lost offline data")
	}
}

// A sync in progress must not hide verified public content, but its fresh
// visibility check must not extend the previous snapshot's proof either.
func TestPublicSnapshotStaysReadableDuringSync(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	connectFixture(t, s, f, false)
	if _, e := s.page("handbook/README.md", true); e != nil {
		t.Fatal("initial public article unavailable", e)
	}
	c := s.connections()[0]
	public := false
	info := github.RepositoryInfo{ID: c.RepositoryID, Private: &public}
	if e := s.connectionStatusFor(c, "syncing", "verified", "", &info, time.Time{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.page("handbook/README.md", true); e != nil {
		t.Fatal("public article hidden while syncing", e)
	}
	if s.connections()[0].VerifiedAt != c.VerifiedAt {
		t.Fatal("sync start extended the previous snapshot's visibility proof")
	}
	private := true
	info.Private = &private
	if e := s.connectionStatusFor(c, "syncing", "verified", "", &info, time.Time{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.page("handbook/README.md", true); e == nil {
		t.Fatal("repository turned private but stayed public during sync")
	}
	if _, e := s.page("handbook/README.md", false); e != nil {
		t.Fatal("private local copy lost", e)
	}
}
