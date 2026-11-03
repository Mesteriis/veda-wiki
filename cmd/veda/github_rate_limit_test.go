package main

import (
	"context"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimitStopsOtherConnectionsUsingSameAccount(t *testing.T) {
	s := testStore(t)
	one := githubtest.New(t, map[string]string{"docs/a.md": "# A"})
	two := githubtest.New(t, map[string]string{"docs/b.md": "# B"})
	two.Modify(func() { two.Name = "org/second"; two.RepoID = 42 })
	var limited atomic.Bool
	var secondCalls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			if limited.Load() {
				secondCalls.Add(1)
			}
			two.Serve(w, r)
			return
		}
		if limited.Load() {
			w.Header().Set("Retry-After", "300")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		one.Serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, github.NewForTest(gateway.URL, gateway.Client()))
	for _, in := range []connectRequest{{ID: "one", Repository: "org/handbook"}, {ID: "two", Repository: "org/second"}} {
		if _, err := m.connect(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	m.Once(context.Background())
	if _, err := s.page("two/docs/b.md", true); err != nil {
		t.Fatal("second source was not initially public", err)
	}
	limited.Store(true)
	m.Once(context.Background())
	if secondCalls.Load() != 0 {
		t.Fatal("second source called GitHub during shared cooldown")
	}
	if _, err := s.page("two/docs/b.md", true); err == nil {
		t.Fatal("shared cooldown left the other source publicly verified")
	}
	if _, err := s.page("two/docs/b.md", false); err != nil {
		t.Fatal("shared cooldown lost local content", err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range reopened.connections() {
		if c.LastError != "rate_limited" {
			t.Fatalf("missing cooldown for %s", c.ID)
		}
		if err := reopened.retryConnection(c.ID); err != ErrConflict {
			t.Fatalf("manual retry of %s bypassed cooldown", c.ID)
		}
	}
	if err := reopened.pauseConnection("two", true); err != nil {
		t.Fatal(err)
	}
	if err := reopened.pauseConnection("two", false); err != nil {
		t.Fatal(err)
	}
	if err := reopened.retryConnection("two"); err != ErrConflict {
		t.Fatal("pause bypassed cooldown", err)
	}
}

func TestRateLimitRetryDeadlineSurvivesRestart(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	connectFixture(t, s, f, false)
	due := time.Now().Add(20 * time.Minute)
	if err := s.connectionStatusFor(s.connections()[0], "error", "unknown", "rate_limited", nil, due); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	c := reopened.connections()[0]
	got, err := time.Parse(time.RFC3339Nano, c.NextAttempt)
	if err != nil || got.Before(due) {
		t.Fatal("retry deadline lost", err)
	}
	if err = reopened.retryConnection("handbook"); err != ErrConflict {
		t.Fatal("manual retry bypassed limit", err)
	}
}

func TestConfigureKeepsRateLimitForSameAccount(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	m := connectFixture(t, s, f, false)
	due := time.Now().Add(20 * time.Minute)
	if err := s.connectionStatusFor(s.connections()[0], "error", "unknown", "rate_limited", nil, due); err != nil {
		t.Fatal(err)
	}
	c := s.connections()[0]
	write := false
	updated, err := m.configure(context.Background(), configureRequest{ID: c.ID, Write: &write, Expected: &c.ConfigRevision})
	if err != nil {
		t.Fatal(err)
	}
	deadline, err := time.Parse(time.RFC3339Nano, updated.NextAttempt)
	if err != nil || deadline.Before(due) {
		t.Fatal("configuration discarded cooldown")
	}
	write = true
	if _, err = m.configure(context.Background(), configureRequest{ID: c.ID, Write: &write, Expected: &updated.ConfigRevision}); err != ErrConflict {
		t.Fatal("configuration bypassed rate limit", err)
	}
}
