// Package githubtest provides an in-memory GitHub server for tests.
package githubtest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github"
)

// Access is the only token the fake accepts; it is not a real credential.
const Access = "fixture-only-token-not-a-real-credential"

// Commit is a fake commit: the tree it names and its parent.
type Commit struct{ Tree, Parent string }

// Fake is an in-memory GitHub REST and GraphQL server for tests. Its exported
// fields are fixture knobs; change them inside Modify.
type Fake struct {
	Mu           sync.Mutex
	Private      bool
	Denied       bool
	Fail         bool
	Reject       bool
	LoseResponse bool
	AfterCommit  func()
	Head         string
	Counter      int
	Writes       int
	BlobWrites   int
	Blobs        map[string][]byte
	Trees        map[string]map[string]string
	Commits      map[string]Commit
	Server       *httptest.Server
	Force        bool
	Name         string
	RepoID       int64
}

// SHA returns the deterministic fake object ID for s.
func SHA(s string) string { return github.BlobDigest([]byte(s), 40) }

// New starts a fake GitHub server serving files as its first commit.
func New(t *testing.T, files map[string]string) *Fake {
	t.Helper()
	f := &Fake{Name: "org/handbook", RepoID: 41, Blobs: map[string][]byte{}, Trees: map[string]map[string]string{}, Commits: map[string]Commit{}}
	f.Advance(files)
	f.Server = httptest.NewServer(http.HandlerFunc(f.Serve))
	t.Cleanup(f.Server.Close)
	return f
}

// Advance records a new commit whose tree holds exactly files.
func (f *Fake) Advance(files map[string]string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	refs := map[string]string{}
	for p, text := range files {
		b := []byte(text)
		sha := github.BlobDigest(b, 40)
		f.Blobs[sha] = b
		refs[p] = sha
	}
	f.Counter++
	tree := SHA(fmt.Sprint("tree", f.Counter))
	f.Trees[tree] = refs
	commit := SHA(fmt.Sprint("commit", f.Counter))
	f.Commits[commit] = Commit{Tree: tree, Parent: f.Head}
	f.Head = commit
}

// Contents returns the files at the current head.
func (f *Fake) Contents() map[string]string {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	out := map[string]string{}
	for p, sha := range f.Trees[f.Commits[f.Head].Tree] {
		out[p] = string(f.Blobs[sha])
	}
	return out
}

// Client returns a GitHub client that talks to this fake server.
func (f *Fake) Client() *github.Client {
	return github.NewForTest(f.Server.URL, f.Server.Client())
}

// Serve answers the GitHub API requests the client makes.
func (f *Fake) Serve(w http.ResponseWriter, r *http.Request) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	respond := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	if f.Fail {
		http.Error(w, "fixture outage", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/user" {
		if r.Header.Get("Authorization") != "Bearer "+Access {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		respond(map[string]string{"login": "operator"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/app/installations/") {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			http.Error(w, "JWT missing", http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			var input struct {
				Repositories []string          `json:"repositories"`
				Permissions  map[string]string `json:"permissions"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			if len(input.Repositories) != 1 || input.Repositories[0] != "handbook" {
				http.Error(w, "unscoped token", 400)
				return
			}
			respond(map[string]any{"token": Access, "expires_at": "2099-01-01T00:00:00Z"})
			return
		}
		respond(map[string]any{"app_id": 7, "account": map[string]string{"login": "org"}, "suspended_at": nil})
		return
	}
	if f.Denied || f.Private && r.Header.Get("Authorization") != "Bearer "+Access {
		http.Error(w, "denied", 404)
		return
	}
	route := strings.TrimPrefix(r.URL.Path, "/repos/"+f.Name)
	switch {
	case r.URL.Path == "/graphql" && r.Method == "POST":
		var request struct {
			Query     string `json:"query"`
			Variables struct {
				Input struct {
					RepositoryID string `json:"repositoryId"`
					Updates      []struct {
						Name   string `json:"name"`
						Before string `json:"beforeOid"`
						After  string `json:"afterOid"`
						Force  bool   `json:"force"`
					} `json:"refUpdates"`
				} `json:"input"`
			} `json:"variables"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if strings.HasPrefix(request.Query, "query") {
			respond(map[string]any{"data": map[string]any{"repository": map[string]string{"id": "fixture-node-" + f.Name}}})
			return
		}
		updates := request.Variables.Input.Updates
		if request.Variables.Input.RepositoryID != "fixture-node-"+f.Name || len(updates) != 1 {
			http.Error(w, "invalid mutation", 400)
			return
		}
		u := updates[0]
		f.Force = f.Force || u.Force
		c, ok := f.Commits[u.After]
		if f.Reject || !ok || u.Name != "refs/heads/main" || u.Before != f.Head || c.Parent != u.Before || u.Force {
			http.Error(w, "head changed", http.StatusUnprocessableEntity)
			return
		}
		f.Head = u.After
		f.Writes++
		if f.LoseResponse {
			f.LoseResponse = false
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		respond(map[string]any{"data": map[string]any{"updateRefs": map[string]any{"clientMutationId": nil}}})
	case r.URL.Path == "/repos/"+f.Name:
		respond(map[string]any{
			"id":             f.RepoID,
			"full_name":      f.Name,
			"private":        f.Private,
			"visibility":     map[bool]string{false: "public", true: "private"}[f.Private],
			"default_branch": "main",
			"permissions":    map[string]bool{"push": true},
		})
	case route == "/git/ref/heads/main":
		respond(map[string]any{"object": map[string]string{"sha": f.Head, "type": "commit"}})
	case strings.HasPrefix(route, "/git/commits/"):
		sha := strings.TrimPrefix(route, "/git/commits/")
		c, ok := f.Commits[sha]
		if !ok {
			http.NotFound(w, r)
			return
		}
		respond(map[string]any{"sha": sha, "tree": map[string]string{"sha": c.Tree}})
	case strings.HasPrefix(route, "/git/trees/"):
		sha := strings.TrimPrefix(route, "/git/trees/")
		files, ok := f.Trees[sha]
		if !ok {
			http.NotFound(w, r)
			return
		}
		entries := []github.TreeEntry{}
		dirs := map[string]bool{}
		for p, blob := range files {
			entries = append(entries, github.TreeEntry{Path: p, Mode: "100644", Type: "blob", SHA: blob, Size: int64(len(f.Blobs[blob]))})
			for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
				dirs[dir] = true
			}
		}
		for dir := range dirs {
			entries = append(entries, github.TreeEntry{Path: dir, Mode: "040000", Type: "tree", SHA: SHA(dir)})
		}
		respond(github.Tree{SHA: sha, Tree: entries})
	case strings.HasPrefix(route, "/git/blobs/"):
		sha := strings.TrimPrefix(route, "/git/blobs/")
		b, ok := f.Blobs[sha]
		if !ok {
			http.NotFound(w, r)
			return
		}
		respond(map[string]any{"sha": sha, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(b), "size": len(b)})
	case route == "/git/blobs" && r.Method == "POST":
		f.BlobWrites++
		var in struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		b := []byte(in.Content)
		if in.Encoding == "base64" {
			var err error
			b, err = base64.StdEncoding.DecodeString(in.Content)
			if err != nil {
				http.Error(w, "bad encoding", 400)
				return
			}
		}
		sha := github.BlobDigest(b, 40)
		f.Blobs[sha] = b
		respond(map[string]string{"sha": sha})
	case route == "/git/trees" && r.Method == "POST":
		var in struct {
			Base string `json:"base_tree"`
			Tree []struct {
				Path string  `json:"path"`
				SHA  *string `json:"sha"`
			} `json:"tree"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		files := map[string]string{}
		base, ok := f.Trees[in.Base]
		if !ok {
			http.Error(w, "base missing", http.StatusUnprocessableEntity)
			return
		}
		for k, v := range base {
			files[k] = v
		}
		for _, e := range in.Tree {
			if e.SHA == nil {
				if _, exists := files[e.Path]; !exists {
					http.Error(w, "file missing", http.StatusUnprocessableEntity)
					return
				}
				delete(files, e.Path)
			} else {
				files[e.Path] = *e.SHA
			}
		}
		f.Counter++
		sha := SHA(fmt.Sprint("new-tree", f.Counter))
		f.Trees[sha] = files
		respond(map[string]string{"sha": sha})
	case route == "/git/commits" && r.Method == "POST":
		var in struct {
			Tree    string   `json:"tree"`
			Parents []string `json:"parents"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if len(in.Parents) != 1 {
			http.Error(w, "parent missing", http.StatusUnprocessableEntity)
			return
		}
		f.Counter++
		sha := SHA(fmt.Sprint("new-commit", f.Counter))
		f.Commits[sha] = Commit{Tree: in.Tree, Parent: in.Parents[0]}
		if f.AfterCommit != nil {
			hook := f.AfterCommit
			f.AfterCommit = nil
			hook()
		}
		respond(map[string]string{"sha": sha})
	case route == "/git/refs/heads/main" && r.Method == "PATCH":
		var in struct {
			SHA   string `json:"sha"`
			Force bool   `json:"force"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.Force = f.Force || in.Force
		c, ok := f.Commits[in.SHA]
		if f.Reject || !ok || c.Parent != f.Head || in.Force {
			http.Error(w, "head changed", http.StatusUnprocessableEntity)
			return
		}
		f.Head = in.SHA
		f.Writes++
		if f.LoseResponse {
			f.LoseResponse = false
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		respond(map[string]string{"sha": in.SHA})
	default:
		http.NotFound(w, r)
	}
}

// Modify runs fn while holding the fake's lock.
func (f *Fake) Modify(fn func()) { f.Mu.Lock(); defer f.Mu.Unlock(); fn() }

// WriteCount returns the number of write requests served.
func (f *Fake) WriteCount() int { f.Mu.Lock(); defer f.Mu.Unlock(); return f.Writes }
