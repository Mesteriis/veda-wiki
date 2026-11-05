package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github"
)

func repositoryJSON(id int64, name string) map[string]any {
	return map[string]any{"id": id, "full_name": name, "private": true, "visibility": "private", "default_branch": "main"}
}

func TestCallReportsRepositoryMoved(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/repos/org/old" {
				w.Header().Set("Location", srv.URL+"/repositories/42")
				w.WriteHeader(status)
				return
			}
			t.Errorf("redirect followed to %s", r.URL.Path)
		}))
		client := github.NewForTest(srv.URL, &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }})
		_, err := client.Repository(context.Background(), "org/old", "")
		srv.Close()
		var remote *github.RemoteError
		if !errors.As(err, &remote) || remote.Code != "repository_moved" || remote.Status != status {
			t.Fatalf("status %d: expected repository_moved, got %v", status, err)
		}
	}
}

func TestRepositoryByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repositories/42" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret-token-value-123" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(repositoryJSON(42, "org/renamed"))
	}))
	defer srv.Close()
	client := github.NewForTest(srv.URL, srv.Client())
	info, err := client.RepositoryByID(context.Background(), 42, "secret-token-value-123")
	if err != nil {
		t.Fatal(err)
	}
	if info.FullName != "org/renamed" || info.ID != 42 || info.Private == nil || !*info.Private {
		t.Fatalf("unexpected repository %+v", info)
	}
	_, err = client.RepositoryByID(context.Background(), 43, "secret-token-value-123")
	var remote *github.RemoteError
	if !errors.As(err, &remote) || remote.Code != "repository_unavailable" {
		t.Fatalf("unknown id must be unavailable, got %v", err)
	}
}

func TestRepositoryByIDRejectsOtherID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(repositoryJSON(7, "org/other"))
	}))
	defer srv.Close()
	client := github.NewForTest(srv.URL, srv.Client())
	_, err := client.RepositoryByID(context.Background(), 42, "")
	var remote *github.RemoteError
	if !errors.As(err, &remote) || remote.Code != "invalid_upstream_response" {
		t.Fatalf("an answer for another repository must be rejected, got %v", err)
	}
}
