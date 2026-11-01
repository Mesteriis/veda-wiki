package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureAccess = "fixture-only-token-not-a-real-credential"

type fakeCommit struct{ tree, parent string }
type fakeGitHub struct {
	mu           sync.Mutex
	private      bool
	denied       bool
	fail         bool
	reject       bool
	loseResponse bool
	head         string
	counter      int
	writes       int
	blobs        map[string][]byte
	trees        map[string]map[string]string
	commits      map[string]fakeCommit
	server       *httptest.Server
	force        bool
	tokens       []string
	name         string
	repoID       int64
}

func fakeSHA(s string) string { return gitBlobDigest([]byte(s), 40) }
func newFakeGitHub(t *testing.T, files map[string]string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{name: "org/handbook", repoID: 41, blobs: map[string][]byte{}, trees: map[string]map[string]string{}, commits: map[string]fakeCommit{}}
	f.advance(files)
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}
func (f *fakeGitHub) advance(files map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	refs := map[string]string{}
	for p, text := range files {
		b := []byte(text)
		sha := gitBlobDigest(b, 40)
		f.blobs[sha] = b
		refs[p] = sha
	}
	f.counter++
	tree := fakeSHA(fmt.Sprint("tree", f.counter))
	f.trees[tree] = refs
	commit := fakeSHA(fmt.Sprint("commit", f.counter))
	f.commits[commit] = fakeCommit{tree: tree, parent: f.head}
	f.head = commit
}
func (f *fakeGitHub) contents() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for p, sha := range f.trees[f.commits[f.head].tree] {
		out[p] = string(f.blobs[sha])
	}
	return out
}
func (f *fakeGitHub) client() *GitHubClient {
	return &GitHubClient{base: f.server.URL, http: f.server.Client()}
}
func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	respond := func(v any) { jsonResponse(w, 200, v) }
	if f.fail {
		http.Error(w, "fixture outage", 503)
		return
	}
	if r.URL.Path == "/user" {
		if r.Header.Get("Authorization") != "Bearer "+fixtureAccess {
			http.Error(w, "denied", 401)
			return
		}
		respond(map[string]string{"login": "operator"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/app/installations/") {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			http.Error(w, "JWT missing", 401)
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
			respond(map[string]any{"token": fixtureAccess, "expires_at": "2099-01-01T00:00:00Z"})
			return
		}
		respond(map[string]any{"app_id": 7, "account": map[string]string{"login": "org"}, "suspended_at": nil})
		return
	}
	if f.denied || f.private && r.Header.Get("Authorization") != "Bearer "+fixtureAccess {
		http.Error(w, "denied", 404)
		return
	}
	route := strings.TrimPrefix(r.URL.Path, "/repos/"+f.name)
	switch {
	case r.URL.Path == "/repos/"+f.name:
		respond(map[string]any{"id": f.repoID, "full_name": f.name, "private": f.private, "visibility": map[bool]string{false: "public", true: "private"}[f.private], "default_branch": "main", "permissions": map[string]bool{"push": true}})
	case route == "/git/ref/heads/main":
		respond(map[string]any{"object": map[string]string{"sha": f.head, "type": "commit"}})
	case strings.HasPrefix(route, "/git/commits/"):
		sha := strings.TrimPrefix(route, "/git/commits/")
		c, ok := f.commits[sha]
		if !ok {
			http.NotFound(w, r)
			return
		}
		respond(map[string]any{"sha": sha, "tree": map[string]string{"sha": c.tree}})
	case strings.HasPrefix(route, "/git/trees/"):
		sha := strings.TrimPrefix(route, "/git/trees/")
		files, ok := f.trees[sha]
		if !ok {
			http.NotFound(w, r)
			return
		}
		entries := []gitTreeEntry{}
		dirs := map[string]bool{}
		for p, blob := range files {
			entries = append(entries, gitTreeEntry{Path: p, Mode: "100644", Type: "blob", SHA: blob, Size: int64(len(f.blobs[blob]))})
			for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
				dirs[dir] = true
			}
		}
		for dir := range dirs {
			entries = append(entries, gitTreeEntry{Path: dir, Mode: "040000", Type: "tree", SHA: fakeSHA(dir)})
		}
		respond(gitTree{SHA: sha, Tree: entries})
	case strings.HasPrefix(route, "/git/blobs/"):
		sha := strings.TrimPrefix(route, "/git/blobs/")
		b, ok := f.blobs[sha]
		if !ok {
			http.NotFound(w, r)
			return
		}
		respond(map[string]any{"sha": sha, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(b), "size": len(b)})
	case route == "/git/blobs" && r.Method == "POST":
		var in struct {
			Content string `json:"content"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		b := []byte(in.Content)
		sha := gitBlobDigest(b, 40)
		f.blobs[sha] = b
		respond(map[string]string{"sha": sha})
	case route == "/git/trees" && r.Method == "POST":
		var in struct {
			Base string `json:"base_tree"`
			Tree []struct {
				Path string `json:"path"`
				SHA  string `json:"sha"`
			} `json:"tree"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		files := map[string]string{}
		base, ok := f.trees[in.Base]
		if !ok {
			http.Error(w, "base missing", 422)
			return
		}
		for k, v := range base {
			files[k] = v
		}
		for _, e := range in.Tree {
			files[e.Path] = e.SHA
		}
		f.counter++
		sha := fakeSHA(fmt.Sprint("new-tree", f.counter))
		f.trees[sha] = files
		respond(map[string]string{"sha": sha})
	case route == "/git/commits" && r.Method == "POST":
		var in struct {
			Tree    string   `json:"tree"`
			Parents []string `json:"parents"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if len(in.Parents) != 1 {
			http.Error(w, "parent missing", 422)
			return
		}
		f.counter++
		sha := fakeSHA(fmt.Sprint("new-commit", f.counter))
		f.commits[sha] = fakeCommit{tree: in.Tree, parent: in.Parents[0]}
		respond(map[string]string{"sha": sha})
	case route == "/git/refs/heads/main" && r.Method == "PATCH":
		var in struct {
			SHA   string `json:"sha"`
			Force bool   `json:"force"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.force = f.force || in.Force
		c, ok := f.commits[in.SHA]
		if f.reject || !ok || c.parent != f.head || in.Force {
			http.Error(w, "head changed", 422)
			return
		}
		f.head = in.SHA
		f.writes++
		if f.loseResponse {
			f.loseResponse = false
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		respond(map[string]string{"sha": in.SHA})
	default:
		http.NotFound(w, r)
	}
}
func connectFixture(t *testing.T, s *Store, f *fakeGitHub, write bool) *SyncManager {
	t.Helper()
	m := newSyncManager(s, f.client())
	account := ""
	if write || f.private {
		if _, e := s.saveAccount(context.Background(), m.github, "operator", GitHubSecret{Kind: "token", Token: fixtureAccess}); e != nil {
			t.Fatal(e)
		}
		account = "operator"
	}
	if _, e := m.connect(context.Background(), connectRequest{ID: "handbook", Repository: "https://github.com/org/handbook", AccountID: account, Write: write}); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	return m
}
func TestGitHubPublicPullAndTrackedProgramScope(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Documentation", "README.md": "not imported", "src/main.go": "package original"})
	connectFixture(t, s, f, false)
	if p, e := s.page("handbook/docs/guide.md", true); e != nil || p.Text != "# Documentation" {
		t.Fatal(p, e)
	}
	if len(s.snapshot(true).Pages) != 1 {
		t.Fatal("program scope")
	}
	if s.db.Sources["handbook"].ConnectionID != "handbook" {
		t.Fatal("connection provenance missing")
	}
	if _, e := s.importBatch(testBatch()); !errorsIsConflictOrInvalid(e) {
		t.Fatal("manual import replaced managed source", e)
	}
}
func errorsIsConflictOrInvalid(e error) bool { return e == ErrInvalid || e == ErrConflict }
func TestGitHubWriteThroughAndRestartDurability(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Original", "src/main.go": "package original"})
	m := connectFixture(t, s, f, true)
	if _, e := s.put("handbook", "docs/guide.md", "# Saved locally", 1); e != nil {
		t.Fatal(e)
	}
	if len(s.jobs()) != 1 {
		t.Fatal("write and outbox not atomic")
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	m = newSyncManager(reopened, f.client())
	m.Once(context.Background())
	if f.contents()["docs/guide.md"] != "# Saved locally" || f.contents()["src/main.go"] != "package original" || f.force {
		t.Fatal("unsafe remote write")
	}
	m.Once(context.Background())
	if reopened.jobs()[0].State != "complete" {
		t.Fatal(reopened.jobs())
	}
}
func TestGitHubLostResponseRecoversWithoutDuplicatePush(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Durable", 1)
	f.modify(func() { f.loseResponse = true })
	m.Once(context.Background())
	if f.writeCount() != 1 || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("prepared write not durable")
	}
	reopened, e := openStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	reopened.retryConnection("handbook")
	newSyncManager(reopened, f.client()).Once(context.Background())
	if f.writeCount() != 1 || reopened.jobs()[0].State != "complete" {
		t.Fatal("ambiguous push duplicated or lost", reopened.jobs())
	}
}
func TestGitHubPrivateVisibilityAndUnpublishedEdits(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, false)
	s.put("handbook", "README.md", "# Private pending canary", 1)
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("private source disclosed")
	}
	f.modify(func() { f.private = false })
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
	f.modify(func() { f.private = true })
	s.retryConnection("handbook")
	m.Once(context.Background())
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("repository downgrade did not revoke public access")
	}
}
func TestGitHubRevocationOutageAndConflictPreserveLocal(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Local", 1)
	f.advance(map[string]string{"veda.yaml": metadataFixture, "README.md": "# Remote"})
	m.Once(context.Background())
	if len(s.conflicts()) != 1 || f.writeCount() != 0 {
		t.Fatal("upstream conflict overwritten")
	}
	f.modify(func() { f.denied = true })
	s.retryConnection("handbook")
	m.Once(context.Background())
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("revoked source remained public")
	}
	if p, e := s.page("handbook/README.md", false); e != nil || p.Text != "# Local" {
		t.Fatal("local availability lost")
	}
}
func TestGitHubCredentialsRedactedAndAppTokensScoped(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"docs/a.md": "# A"})
	account, e := s.saveAccount(context.Background(), f.client(), "operator", GitHubSecret{Kind: "token", Token: fixtureAccess})
	if e != nil || account.SecretRef != "" {
		t.Fatal(account, e)
	}
	state, e := os.ReadFile(path.Join(s.dir, "state.json"))
	if e != nil || strings.Contains(string(state), fixtureAccess) {
		t.Fatal("credential stored in article transaction")
	}
	metadata, _ := json.Marshal(s.accounts())
	if strings.Contains(string(metadata), fixtureAccess) || strings.Contains(string(metadata), "secret_ref") {
		t.Fatal("credential response leaked")
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	secret := GitHubSecret{Kind: "app", AppID: 7, InstallationID: 9, PrivateKey: string(encoded)}
	if login, e := f.client().verifyAccount(context.Background(), secret); e != nil || login != "org" {
		t.Fatal(login, e)
	}
	if _, e = f.client().token(context.Background(), &secret, "org/handbook", true); e != nil {
		t.Fatal(e)
	}
	jwt, e := appJWT(secret)
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatal("invalid JWT")
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if e = rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature); e != nil {
		t.Fatal("JWT signature invalid", e)
	}
}

func TestGitHubCompareAndSwapNeverForcesUpdates(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Original"})
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Local", 1)
	f.modify(func() { f.reject = true })
	m.Once(context.Background())
	if f.writeCount() != 0 || s.jobs()[0].State != "retry" || s.jobs()[0].PreparedCommit == "" {
		t.Fatal("CAS refusal lost work", s.jobs())
	}
	p, _ := s.page("handbook/README.md", false)
	if p.Text != "# Local" {
		t.Fatal("local write lost")
	}
	f.modify(func() { f.reject = false })
	s.retryConnection("handbook")
	m.Once(context.Background())
	m.Once(context.Background())
	if f.writeCount() != 1 || s.jobs()[0].State != "complete" {
		t.Fatal(s.jobs())
	}
}
func TestGitHubMultipleRepositoriesStayIndependent(t *testing.T) {
	s := testStore(t)
	one := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Public one"})
	two := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Private two"})
	two.modify(func() { two.name = "org/second"; two.repoID = 42; two.private = true })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			two.serve(w, r)
		} else {
			one.serve(w, r)
		}
	}))
	defer gateway.Close()
	client := &GitHubClient{base: gateway.URL, http: gateway.Client()}
	m := newSyncManager(s, client)
	s.saveAccount(context.Background(), client, "operator", GitHubSecret{Kind: "token", Token: fixtureAccess})
	if _, e := m.connect(context.Background(), connectRequest{ID: "one", Repository: "org/handbook"}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.connect(context.Background(), connectRequest{ID: "two", Repository: "org/second"}); e == nil {
		t.Fatal("connector guessed a saved credential")
	}
	if _, e := m.connect(context.Background(), connectRequest{ID: "two", Repository: "org/second", AccountID: "operator"}); e != nil {
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
	if _, e := s.page("two/docs/guide.md", true); e != ErrNotFound {
		t.Fatal("private repository disclosed")
	}
}
func TestGitHubPauseAndExpiredProofFailClosed(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Public"})
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
	s.mu.Lock()
	c := s.db.Connections["handbook"]
	c.VerifiedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
	s.db.Connections[c.ID] = c
	s.mu.Unlock()
	if len(s.snapshot(true).Pages) != 0 {
		t.Fatal("expired verification published")
	}
	if len(s.snapshot(false).Pages) != 1 {
		t.Fatal("owner lost offline data")
	}
}
func TestGitHubManagementAPIProtectsCredentials(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Public"})
	m := newSyncManager(s, f.client())
	w := Wiki{Store: s, Token: "test-owner", Sync: m}
	body := `{"id":"operator","kind":"token","token":"` + fixtureAccess + `"}`
	if r := request(w, "POST", "/api/v1/github/accounts", body, true); r.Code != 403 {
		t.Fatal("LAN HTTP credentials accepted")
	}
	r := httptest.NewRequest("POST", "/api/v1/github/accounts", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Authorization", "Bearer test-owner")
	response := httptest.NewRecorder()
	w.ServeHTTP(response, r)
	if response.Code != 201 || strings.Contains(response.Body.String(), fixtureAccess) || strings.Contains(response.Body.String(), "secret_ref") {
		t.Fatal(response.Code, response.Body)
	}
	for _, route := range []string{"/api/v1/github/accounts", "/api/v1/github/connections", "/api/v1/github/outbox"} {
		if r := request(w, "GET", route, "", false); r.Code != 404 {
			t.Fatal(route, r.Code)
		}
	}
	if r := request(w, "POST", "/api/v1/github/connections", `{"id":"handbook","repository":"org/handbook","account_id":"operator","write":true}`, true); r.Code != 202 {
		t.Fatal(r.Code, r.Body)
	}
	m.Once(context.Background())
	if r := request(w, "GET", "/api/v1/github/outbox?limit=0", "", true); r.Code != 400 {
		t.Fatal(r.Code)
	}
	w.Public = true
	if r := request(w, "GET", "/api/v1/github/accounts", "", true); r.Code != 404 {
		t.Fatal("public listener accepted owner key")
	}
}

func TestGitHubEnableWritesPreservesQueuedLocalArticles(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Original"})
	m := connectFixture(t, s, f, false)
	s.put("handbook", "docs/guide.md", "# Local", 1)
	m.Once(context.Background())
	if s.jobs()[0].State != "blocked" || f.writeCount() != 0 {
		t.Fatal("read-only connection wrote remotely")
	}
	if _, e := s.saveAccount(context.Background(), m.github, "operator", GitHubSecret{Kind: "token", Token: fixtureAccess}); e != nil {
		t.Fatal(e)
	}
	account, write, expected := "operator", true, 1
	c, e := m.configure(context.Background(), configureRequest{ID: "handbook", AccountID: &account, Write: &write, Expected: &expected})
	if e != nil || c.ConfigRevision != 2 {
		t.Fatal(c, e)
	}
	if _, e := m.configure(context.Background(), configureRequest{ID: "handbook", Write: &write, Expected: &expected}); e != ErrConflict {
		t.Fatal("stale configuration accepted", e)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.writeCount() != 1 || s.jobs()[0].State != "complete" {
		t.Fatal(s.jobs())
	}
}

func TestGitHubPrivateQueuedWritesRequirePublicationConsent(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, true)
	s.put("handbook", "README.md", "# Private unsent canary", 1)
	f.modify(func() { f.private = false })
	m.Once(context.Background())
	jobs := s.jobs()
	if f.writeCount() != 0 || len(jobs) != 1 || jobs[0].LastError != "publication_confirmation_required" {
		t.Fatal("private work auto-published", jobs)
	}
	public, e := s.article("handbook", "README.md", true)
	if e != nil || public.Versions[0].Text != "# Upstream" || public.Versions[0].Number != 0 {
		t.Fatal("public history contains private metadata", public, e)
	}
	if e = s.publishJob(jobs[0].ID, 2, false); e != ErrInvalid {
		t.Fatal("implicit publication accepted")
	}
	if e = s.publishJob(jobs[0].ID, 1, true); e != ErrConflict {
		t.Fatal("stale publication accepted")
	}
	if e = s.publishJob(jobs[0].ID, 2, true); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.writeCount() != 1 || s.jobs()[0].State != "complete" {
		t.Fatal(s.jobs())
	}
}

func (f *fakeGitHub) modify(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }
func (f *fakeGitHub) writeCount() int  { f.mu.Lock(); defer f.mu.Unlock(); return f.writes }

func TestGitHubExpiredPublicProofDoesNotApproveQueuedWrite(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Upstream"})
	m := connectFixture(t, s, f, true)
	s.mu.Lock()
	c := s.db.Connections["handbook"]
	c.VerifiedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
	s.db.Connections[c.ID] = c
	s.mu.Unlock()
	if _, e := s.put("handbook", "README.md", "# Unverified edit", 1); e != nil {
		t.Fatal(e)
	}
	m.Once(context.Background())
	jobs := s.jobs()
	if f.writeCount() != 0 || len(jobs) != 1 || jobs[0].LastError != "publication_confirmation_required" {
		t.Fatal("expired proof allowed publication", jobs)
	}
}
