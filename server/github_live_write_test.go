package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This opt-in test uses gh as a credential-owning transport. VEDA receives
// only a synthetic token and an isolated local store, never a real credential.
// It creates retained fixture branches; an explicitly selected second public
// repository is only changed on a new branch, preserving its existing tree.
func TestLiveGitHubBranchMove(t *testing.T) {
	if os.Getenv("VEDA_LIVE_GITHUB_MOVE") != "1" {
		t.Skip("explicit live GitHub acceptance not enabled")
	}
	const repo = "Mesteriis/veda-wiki"
	targetRepo := os.Getenv("VEDA_LIVE_GITHUB_TARGET")
	if targetRepo == "" {
		targetRepo = repo
	}
	if targetRepo != repo && targetRepo != "Mesteriis/mac-tower" {
		t.Fatal("unsupported fixture repository")
	}
	repos := []string{repo, targetRepo}
	content := "# Synthetic source\n\nThis is an acceptance fixture.\n\n[Related](related.md)\n\n![fixture](img/icon.png)\n![duplicate](img/duplicate.png)\n"
	directoryFixture := os.Getenv("VEDA_LIVE_GITHUB_DIRECTORY") == "1"
	if directoryFixture {
		content += "\n[Directory](docs/)\n"
	}
	const related = "# Synthetic related article\n"
	const index = "# Synthetic directory index\n"
	image := smallPNG(t)
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
	infoSource, infoTarget := api("repos/"+repo, nil), api("repos/"+targetRepo, nil)
	for _, info := range []map[string]any{infoSource, infoTarget} {
		if info["private"] != false || info["visibility"] != "public" {
			t.Fatal("fixture repository is not verified public")
		}
	}
	nodeIDs := []string{infoSource["node_id"].(string), infoTarget["node_id"].(string)}
	base := api("repos/"+repo+"/commits/588ff21", nil)["sha"].(string)
	imageSHA := api("repos/"+repo+"/git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString(image), "encoding": "base64"})["sha"].(string)
	treeEntries := []map[string]string{
		{"path": "veda.yaml", "mode": "100644", "type": "blob", "content": metadataFixture},
		{"path": "guide.md", "mode": "100644", "type": "blob", "content": content},
		{"path": "related.md", "mode": "100644", "type": "blob", "content": related},
		{"path": "img/icon.png", "mode": "100644", "type": "blob", "sha": imageSHA},
		{"path": "img/duplicate.png", "mode": "100644", "type": "blob", "sha": imageSHA},
	}
	if directoryFixture {
		treeEntries = append(treeEntries, map[string]string{"path": "docs/index.md", "mode": "100644", "type": "blob", "content": index})
	}
	tree := api("repos/"+repo+"/git/trees", map[string]any{"tree": treeEntries})["sha"].(string)
	commit := api("repos/"+repo+"/git/commits", map[string]any{"message": "VEDA synthetic move fixture", "tree": tree, "parents": []string{base}})["sha"].(string)
	suffix := time.Now().UTC().Format("20060102-150405")
	branches := []string{"feature/veda-live-source-" + suffix, "feature/veda-live-target-" + suffix}
	destinationPath := "moved.md"
	if directoryFixture && targetRepo == repo {
		destinationPath = "veda-fixture/moved.md"
	}
	commits := []string{commit, commit}
	baseEntries := map[string]string{}
	if targetRepo != repo {
		destinationPath = "veda-fixtures/" + suffix + "/moved.md"
		info := api("repos/"+targetRepo, nil)
		targetBase := api("repos/"+targetRepo+"/commits/"+info["default_branch"].(string), nil)
		baseSHA := targetBase["sha"].(string)
		baseTree := targetBase["commit"].(map[string]any)["tree"].(map[string]any)["sha"].(string)
		entries := api("repos/"+targetRepo+"/git/trees/"+baseTree+"?recursive=1", nil)
		if entries["truncated"] != false {
			t.Fatal("target fixture tree incomplete")
		}
		for _, value := range entries["tree"].([]any) {
			item := value.(map[string]any)
			p := item["path"].(string)
			if item["type"] == "blob" && strings.HasPrefix(p, ".github/workflows/") {
				t.Fatal("target contains workflows; refusing to create a fixture branch")
			}
			if p == "veda.yaml" || p == destinationPath {
				t.Fatal("target fixture path already exists")
			}
			if p == "veda-fixtures" && item["type"] != "tree" {
				t.Fatal("fixture directory is not a tree")
			}
			if item["type"] != "tree" {
				baseEntries[p] = item["type"].(string) + ":" + item["mode"].(string) + ":" + item["sha"].(string)
			}
		}
		relatedPath := path.Join(path.Dir(destinationPath), "related.md")
		for _, value := range entries["tree"].([]any) {
			if value.(map[string]any)["path"].(string) == relatedPath {
				t.Fatal("target fixture link already exists")
			}
		}
		fixtureTree := api("repos/"+targetRepo+"/git/trees", map[string]any{"base_tree": baseTree, "tree": []map[string]string{{"path": "veda.yaml", "mode": "100644", "type": "blob", "content": metadataFixture}}})["sha"].(string)
		commits[1] = api("repos/"+targetRepo+"/git/commits", map[string]any{"message": "VEDA synthetic move fixture", "tree": fixtureTree, "parents": []string{baseSHA}})["sha"].(string)
	}
	for i, b := range branches {
		api("repos/"+repos[i]+"/git/refs", map[string]string{"ref": "refs/heads/" + b, "sha": commits[i]})
	}

	var outage atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if outage.Load() {
			http.Error(w, "synthetic outage", http.StatusServiceUnavailable)
			return
		}
		route := strings.TrimPrefix(r.URL.RequestURI(), "/")
		allowedRepo := false
		for _, allowed := range repos {
			if route == "repos/"+allowed || strings.HasPrefix(route, "repos/"+allowed+"/") {
				allowedRepo = true
			}
		}
		if route != "user" && route != "graphql" && !allowedRepo {
			http.Error(w, "route denied", 403)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		allowedWrite := route == "graphql"
		for _, allowed := range repos {
			if route == "repos/"+allowed+"/git/blobs" || route == "repos/"+allowed+"/git/trees" || route == "repos/"+allowed+"/git/commits" {
				allowedWrite = true
			}
		}
		if r.Method != "GET" && (r.Method != "POST" || !allowedWrite) {
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
				if request.Variables.Owner != "Mesteriis" || (request.Variables.Name != "veda-wiki" && !(targetRepo != repo && request.Variables.Name == "mac-tower")) {
					http.Error(w, "repository denied", 403)
					return
				}
			case `mutation($input:UpdateRefsInput!) { updateRefs(input:$input) { clientMutationId } }`:
				if len(request.Variables.Input.Updates) != 1 {
					http.Error(w, "mutation denied", 403)
					return
				}
				update := request.Variables.Input.Updates[0]
				if update.Force || !((request.Variables.Input.RepositoryID == nodeIDs[0] && update.Name == "refs/heads/"+branches[0]) || (request.Variables.Input.RepositoryID == nodeIDs[1] && update.Name == "refs/heads/"+branches[1])) {
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
		if _, err := manager.connect(ctx, connectRequest{ID: id, Repository: repos[i], AccountID: "fixture", Branch: b, Write: true}); err != nil {
			t.Fatalf("connect %s: %v", id, err)
		}
	}
	manager.Once(ctx)
	if os.Getenv("VEDA_LIVE_GITHUB_RECOVERY") == "1" {
		const draftPath, draftText = "synthetic-created.md", "# Synthetic API-created article\n"
		body, _ := json.Marshal(map[string]any{"source_id": "source", "path": draftPath, "text": draftText, "expected_revision": 0})
		response := request(Wiki{Store: store, Token: "test-owner"}, "POST", "/api/v1/articles", string(body), true)
		if response.Code != http.StatusCreated {
			t.Fatal("REST fixture creation failed", response.Code)
		}
		outage.Store(true)
		syncFixtureSource(t, manager, "source")
		article, err := store.article("source", draftPath, false)
		if err != nil || article.Versions[0].Text != draftText {
			t.Fatal("local write unavailable during outage", err)
		}
		if err := migrateSQLite(store.dir); err != nil {
			t.Fatal("fixture migration failed", err)
		}
		store, err = openStore(store.dir)
		if err != nil {
			t.Fatal("fixture reopen failed", err)
		}
		article, err = store.article("source", draftPath, false)
		if err != nil || article.Versions[0].Text != draftText {
			t.Fatal("local write lost after restart", err)
		}
		outage.Store(false)
		manager = newSyncManager(store, client)
		if err := store.retryConnection("source"); err != nil {
			t.Fatal("retry failed", err)
		}
		completed := false
		for i := 0; i < 6; i++ {
			syncFixtureSource(t, manager, "source")
			for _, job := range store.jobs() {
				if job.SourceID == "source" && job.Path == draftPath && job.State == "complete" {
					completed = true
				}
			}
			if completed {
				break
			}
		}
		if !completed {
			t.Fatal("queued write not observed after recovery")
		}
		ref := api("repos/"+repo+"/git/ref/heads/"+branches[0], nil)
		head := ref["object"].(map[string]any)["sha"].(string)
		remoteTree, err := client.tree(ctx, repo, head, fixtureAccess)
		if err != nil {
			t.Fatal("recovered tree unavailable", err)
		}
		if _, err := verifiedTreeFile(remoteTree, draftPath, draftText); err != nil {
			t.Fatal("remote fixture does not match local write")
		}
	}
	if _, err := store.moveArticle(moveRequest{SourceID: "source", SourcePath: "guide.md", ExpectedRevision: 1, DestinationID: "target", DestinationPath: destinationPath, ConfirmMove: true, IncludeLinked: true}); err != nil {
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
		ref := api("repos/"+repos[i]+"/git/ref/heads/"+b, nil)
		head := ref["object"].(map[string]any)["sha"].(string)
		remoteTree, err := client.tree(ctx, repos[i], head, fixtureAccess)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		actual := map[string]string{}
		for _, entry := range remoteTree.Tree {
			found[entry.Path] = true
			actual[entry.Path] = entry.Type + ":" + entry.Mode + ":" + entry.SHA
		}
		if i == 0 && found["guide.md"] {
			t.Fatal("source still exists")
		}
		if i == 0 && !found["related.md"] {
			t.Fatal("linked source article was removed")
		}
		if i == 0 && directoryFixture && !found["docs/index.md"] {
			t.Fatal("linked directory source was removed")
		}
		if i == 1 && !found[destinationPath] {
			t.Fatal("destination absent")
		}
		if i == 1 {
			for path, sha := range baseEntries {
				if actual[path] != sha {
					t.Fatal("target fixture modified existing content")
				}
			}
			if _, err := verifiedTreeFile(remoteTree, destinationPath, content); err != nil {
				t.Fatal("destination contents do not match the fixture")
			}
			imagePath := path.Join(path.Dir(destinationPath), "img/icon.png")
			if _, err := verifiedTreeFile(remoteTree, imagePath, string(image)); err != nil {
				t.Fatal("destination image does not match fixture")
			}
			duplicatePath := path.Join(path.Dir(destinationPath), "img/duplicate.png")
			if _, err := verifiedTreeFile(remoteTree, duplicatePath, string(image)); err != nil {
				t.Fatal("duplicate image does not match fixture")
			}
			relatedPath := path.Join(path.Dir(destinationPath), "related.md")
			if _, err := verifiedTreeFile(remoteTree, relatedPath, related); err != nil {
				t.Fatal("linked fixture differs")
			}
			if directoryFixture {
				indexPath := path.Join(path.Dir(destinationPath), "docs/README.md")
				if _, err := verifiedTreeFile(remoteTree, indexPath, index); err != nil {
					t.Fatal("mapped directory fixture differs")
				}
			}
		}
	}
	t.Logf("live fixture branches retained: %s:%s, %s:%s", repos[0], branches[0], repos[1], branches[1])
}
