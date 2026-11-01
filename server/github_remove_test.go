package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubRemovePreparesBeforeNonForceUpdate(t *testing.T) {
	base, tree, nextTree, commit := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	for _, prepareFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "prepare failure"}[prepareFails], func(t *testing.T) {
			prepared, patched := false, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				switch r.URL.Path {
				case "/repos/org/repo/git/trees":
					entries, ok := body["tree"].([]any)
					if !ok || len(entries) != 1 || body["base_tree"] != tree {
						t.Error("invalid base tree or entries")
						return
					}
					entry, ok := entries[0].(map[string]any)
					sha, exists := entry["sha"]
					if !ok || !exists || sha != nil || entry["path"] != "docs/a.md" {
						t.Error("deletion must use null SHA for the selected path")
						return
					}
					jsonResponse(w, 200, map[string]string{"sha": nextTree})
				case "/repos/org/repo/git/commits":
					parents, ok := body["parents"].([]any)
					if !ok || len(parents) != 1 || parents[0] != base || body["tree"] != nextTree {
						t.Error("invalid commit parent or tree")
						return
					}
					jsonResponse(w, 200, map[string]string{"sha": commit})
				case "/repos/org/repo/git/refs/heads/main":
					patched = true
					if !prepared || body["sha"] != commit || body["force"] != false {
						t.Error("unsafe reference update")
					}
					jsonResponse(w, 200, map[string]string{})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			client := &GitHubClient{base: srv.URL, http: srv.Client()}
			source := gitTree{SHA: tree, Tree: []gitTreeEntry{{Path: "docs/a.md", Mode: "100644", Type: "blob", SHA: gitBlobDigest([]byte("# Source"), 40)}}}
			got, err := client.remove(context.Background(), "org/repo", "main", base, source, "docs/a.md", "# Source", "", func(sha string) error {
				if sha != commit {
					t.Error("wrong prepared commit")
				}
				if prepareFails {
					return errors.New("persist failed")
				}
				prepared = true
				return nil
			})
			if prepareFails {
				if err == nil || patched {
					t.Fatal("failed preparation must prevent reference update")
				}
			} else if err != nil || got != commit || !patched {
				t.Fatalf("remove: commit %q, patched %v, error %v", got, patched, err)
			}
		})
	}
}

func TestGitHubRemoveRejectsUnverifiedSource(t *testing.T) {
	f := newFakeGitHub(t, map[string]string{"docs/a.md": "# Source"})
	client := f.client()
	head, err := client.head(context.Background(), f.name, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := client.tree(context.Background(), f.name, head, "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := false
	_, err = client.remove(context.Background(), f.name, "main", head, tree, "docs/a.md", "# Changed", "", func(string) error { prepared = true; return nil })
	if err != ErrConflict || prepared || f.writeCount() != 0 {
		t.Fatalf("changed source accepted: %v", err)
	}
	tree.Truncated = true
	_, err = client.remove(context.Background(), f.name, "main", head, tree, "docs/a.md", "# Source", "", func(string) error { prepared = true; return nil })
	if err != ErrInvalid || prepared || f.writeCount() != 0 {
		t.Fatalf("truncated tree accepted: %v", err)
	}
}

func TestGitHubRemoveRejectsConcurrentBranchUpdate(t *testing.T) {
	f := newFakeGitHub(t, map[string]string{"docs/a.md": "# Source"})
	client := f.client()
	head, err := client.head(context.Background(), f.name, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := client.tree(context.Background(), f.name, head, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.remove(context.Background(), f.name, "main", head, tree, "docs/a.md", "# Source", "", func(string) error {
		f.advance(map[string]string{"docs/a.md": "# Remote edit"})
		return nil
	})
	if err == nil || f.contents()["docs/a.md"] != "# Remote edit" || f.writeCount() != 0 {
		t.Fatalf("concurrent edit overwritten: %v", err)
	}
}

func TestGitHubRemovePreservesOtherFiles(t *testing.T) {
	f := newFakeGitHub(t, map[string]string{"docs/a.md": "# Source", "docs/other.md": "# Other"})
	client := f.client()
	head, err := client.head(context.Background(), f.name, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := client.tree(context.Background(), f.name, head, "")
	if err != nil {
		t.Fatal(err)
	}
	var prepared string
	commit, err := client.remove(context.Background(), f.name, "main", head, tree, "docs/a.md", "# Source", "", func(value string) error { prepared = value; return nil })
	files := f.contents()
	if err != nil || commit != prepared || f.writeCount() != 1 || len(files) != 1 || files["docs/other.md"] != "# Other" {
		t.Fatalf("deletion failed to preserve other files: %v", err)
	}
}
