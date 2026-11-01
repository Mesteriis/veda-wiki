package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This opt-in test uses gh as a credential-owning transport. VEDA receives
// only a synthetic token and an isolated local store, never a real credential.
// It creates two retained fixture branches in the public VEDA repository.
func TestLiveGitHubBranchMove(t *testing.T) {
	if os.Getenv("VEDA_LIVE_GITHUB_MOVE") != "1" {
		t.Skip("explicit live GitHub acceptance not enabled")
	}
	const repo = "Mesteriis/veda-wiki"
	const content = "# Synthetic source\n\nThis is an acceptance fixture.\n"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	gh := func(route, method string, body []byte) ([]byte, error) {
		args := []string{"api", route, "--method", method}
		if body != nil {
			args = append(args, "--input", "-")
		}
		cmd := exec.CommandContext(ctx, "gh", args...)
		cmd.Stdin = bytes.NewReader(body)
		return cmd.Output()
	}
	api := func(route string, body any) map[string]any {
		t.Helper()
		var raw []byte
		method := "GET"
		if body != nil {
			method = "POST"
			raw, _ = json.Marshal(body)
		}
		output, err := gh(route, method, raw)
		if err != nil {
			t.Fatalf("GitHub fixture operation failed: %s", route)
		}
		var result map[string]any
		if json.Unmarshal(output, &result) != nil {
			t.Fatal("invalid fixture response")
		}
		return result
	}
	login := api("user", nil)["login"]
	if login != "Mesteriis" && login != "mesteriis" {
		t.Fatal("fixture requires the Mesteriis account")
	}
	nodeID := api("repos/"+repo, nil)["node_id"].(string)
	base := api("repos/"+repo+"/commits/588ff21", nil)["sha"].(string)
	tree := api("repos/"+repo+"/git/trees", map[string]any{"tree": []map[string]string{
		{"path": "veda.yaml", "mode": "100644", "type": "blob", "content": metadataFixture},
		{"path": "guide.md", "mode": "100644", "type": "blob", "content": content},
	}})["sha"].(string)
	commit := api("repos/"+repo+"/git/commits", map[string]any{"message": "VEDA synthetic move fixture", "tree": tree, "parents": []string{base}})["sha"].(string)
	suffix := time.Now().UTC().Format("20060102-150405")
	branches := []string{"feature/veda-live-source-" + suffix, "feature/veda-live-target-" + suffix}
	for _, b := range branches {
		api("repos/"+repo+"/git/refs", map[string]string{"ref": "refs/heads/" + b, "sha": commit})
	}

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := strings.TrimPrefix(r.URL.RequestURI(), "/")
		if route != "user" && route != "graphql" && route != "repos/"+repo && !strings.HasPrefix(route, "repos/"+repo+"/") {
			http.Error(w, "route denied", 403)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		if r.Method != "GET" && (r.Method != "POST" || (route != "graphql" && route != "repos/"+repo+"/git/blobs" && route != "repos/"+repo+"/git/trees" && route != "repos/"+repo+"/git/commits")) {
			http.Error(w, "write denied", 403)
			return
		}
		if route == "graphql" {
			var request struct {
				Query     string `json:"query"`
				Variables struct {
					Owner string `json:"owner"`
					Name  string `json:"name"`
					Input struct {
						RepositoryID string `json:"repositoryId"`
						Updates      []struct {
							Name  string `json:"name"`
							Force bool   `json:"force"`
						} `json:"refUpdates"`
					} `json:"input"`
				} `json:"variables"`
			}
			if r.Method != "POST" || json.Unmarshal(body, &request) != nil {
				http.Error(w, "mutation denied", 403)
				return
			}
			switch request.Query {
			case `query($owner:String!, $name:String!) { repository(owner:$owner, name:$name) { id } }`:
				if request.Variables.Owner != "Mesteriis" || request.Variables.Name != "veda-wiki" {
					http.Error(w, "repository denied", 403)
					return
				}
			case `mutation($input:UpdateRefsInput!) { updateRefs(input:$input) { clientMutationId } }`:
				if request.Variables.Input.RepositoryID != nodeID || len(request.Variables.Input.Updates) != 1 {
					http.Error(w, "mutation denied", 403)
					return
				}
				update := request.Variables.Input.Updates[0]
				if update.Force || (update.Name != "refs/heads/"+branches[0] && update.Name != "refs/heads/"+branches[1]) {
					http.Error(w, "branch denied", 403)
					return
				}
			default:
				http.Error(w, "query denied", 403)
				return
			}
		}
		if r.Method == "GET" {
			body = nil
		}
		result, err := gh(route, r.Method, body)
		if err != nil {
			http.Error(w, "upstream failure", 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result)
	}))
	defer proxy.Close()
	store := testStore(t)
	client := &GitHubClient{base: proxy.URL, http: proxy.Client()}
	manager := newSyncManager(store, client)
	if _, err := store.saveAccount(ctx, client, "fixture", GitHubSecret{Kind: "token", Token: fixtureAccess}); err != nil {
		t.Fatalf("account: %v", err)
	}
	for i, b := range branches {
		id := []string{"source", "target"}[i]
		if _, err := manager.connect(ctx, connectRequest{ID: id, Repository: repo, AccountID: "fixture", Branch: b, Write: true}); err != nil {
			t.Fatalf("connect %s: %v", id, err)
		}
	}
	manager.Once(ctx)
	if _, err := store.moveArticle(moveRequest{SourceID: "source", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: "moved.md", ConfirmMove: true}); err != nil {
		t.Fatalf("move: %v", err)
	}
	syncFixtureSource(t, manager, "target")
	syncFixtureSource(t, manager, "source")
	if got := store.moves()[0].State; got != "waiting_destination" {
		t.Fatalf("source advanced before destination observation: %s", got)
	}
	ref := api("repos/"+repo+"/git/ref/heads/"+branches[0], nil)
	head := ref["object"].(map[string]any)["sha"].(string)
	beforeTree, err := client.tree(ctx, repo, head, fixtureAccess)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifiedTreeFile(beforeTree, "guide.md", content); err != nil {
		t.Fatal("source changed before destination observation")
	}
	syncFixtureSource(t, manager, "target")
	if got := store.moves()[0].State; got != "waiting_source_delete" {
		t.Fatalf("destination not observed: %s", got)
	}
	syncFixtureSource(t, manager, "source")
	if got := store.moves()[0].State; got != "deleting_source" {
		t.Fatalf("deletion not prepared: %s", got)
	}
	syncFixtureSource(t, manager, "source")
	if got := store.moves()[0].State; got != "complete" {
		t.Fatalf("move incomplete: %s", got)
	}
	for i, b := range branches {
		ref := api("repos/"+repo+"/git/ref/heads/"+b, nil)
		head := ref["object"].(map[string]any)["sha"].(string)
		remoteTree, err := client.tree(ctx, repo, head, fixtureAccess)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, entry := range remoteTree.Tree {
			found[entry.Path] = true
		}
		if i == 0 && found["guide.md"] {
			t.Fatal("source still exists")
		}
		if i == 1 && !found["moved.md"] {
			t.Fatal("destination absent")
		}
		if i == 1 {
			if _, err := verifiedTreeFile(remoteTree, "moved.md", content); err != nil {
				t.Fatal("destination contents do not match the fixture")
			}
		}
	}
	t.Logf("live fixture branches retained: %s, %s", branches[0], branches[1])
}
