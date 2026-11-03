package github

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
	g := NewForTest(srv.URL, srv.Client())
	err := g.call(context.Background(), "GET", "/fixture", "", nil, nil)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "rate_limited" || remote.RetryAt.Before(reset) {
		t.Fatalf("429 not preserved with reset deadline: %v", err)
	}
	now := time.Now()
	if got := githubRetryAt(http.Header{}, now); got.Sub(now) != time.Minute {
		t.Fatalf("missing headers should wait a minute: %v", got.Sub(now))
	}
}
