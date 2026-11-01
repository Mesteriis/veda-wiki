package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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
