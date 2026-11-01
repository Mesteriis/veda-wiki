package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubRateLimitResponseAndDeadline(t *testing.T) {
	reset := time.Now().Add(20 * time.Minute).Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	g := &GitHubClient{base: srv.URL, http: srv.Client()}
	err := g.call(context.Background(), "GET", "/fixture", "", nil, nil)
	var remote *remoteError
	if !errors.As(err, &remote) || remote.Code != "rate_limited" || remote.RetryAt.Before(reset) {
		t.Fatalf("429 not preserved with reset deadline: %v", err)
	}
	now := time.Now()
	if got := githubRetryAt(http.Header{}, now); got.Sub(now) != time.Minute {
		t.Fatalf("missing headers should wait a minute: %v", got.Sub(now))
	}
}

func TestRateLimitStopsOtherConnectionsUsingSameAccount(t *testing.T) {
	s := testStore(t)
	one := newFakeGitHub(t, map[string]string{"docs/a.md": "# A"})
	two := newFakeGitHub(t, map[string]string{"docs/b.md": "# B"})
	two.modify(func() { two.name = "org/second"; two.repoID = 42 })
	var limited atomic.Bool
	var secondCalls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			if limited.Load() {
				secondCalls.Add(1)
			}
			two.serve(w, r)
			return
		}
		if limited.Load() {
			w.Header().Set("Retry-After", "300")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		one.serve(w, r)
	}))
	defer gateway.Close()
	m := newSyncManager(s, &GitHubClient{base: gateway.URL, http: gateway.Client()})
	for _, in := range []connectRequest{{ID: "one", Repository: "org/handbook"}, {ID: "two", Repository: "org/second"}} {
		if _, err := m.connect(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	limited.Store(true)
	m.Once(context.Background())
	if secondCalls.Load() != 0 {
		t.Fatal("second source called GitHub during shared cooldown")
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
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	connectFixture(t, s, f, false)
	due := time.Now().Add(20 * time.Minute)
	if err := s.connectionStatusRetry("handbook", "error", "unknown", "rate_limited", nil, due); err != nil {
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
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "guide.md": "# Fixture"})
	m := connectFixture(t, s, f, false)
	due := time.Now().Add(20 * time.Minute)
	if err := s.connectionStatusRetry("handbook", "error", "unknown", "rate_limited", nil, due); err != nil {
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
