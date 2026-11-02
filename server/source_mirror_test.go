package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mirrorFixture(t *testing.T) (*Store, *SyncManager, *fakeGitHub) {
	t.Helper()
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, true)
	return s, m, f
}
func TestMirrorBackgroundUpdatesAndPause(t *testing.T) {
	s, m, f := mirrorFixture(t)
	enabled := true
	expected := 0
	conf, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &enabled, ExpectedRevision: &expected})
	if err != nil || conf.Revision != 1 || s.db.Version != 19 {
		t.Fatal("enable failed", err)
	}
	m.Once(context.Background())
	if s.db.Articles["handbook/a.md"].ReconcileBase == nil || s.mirrors()[0].State != "ready" {
		t.Fatal("baseline not created")
	}
	if _, err = s.put("local", "a.md", "# Changed", 1); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if len(s.jobs()) != 1 || s.jobs()[0].State != "pending" {
		t.Fatal("update not queued")
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.contents()["a.md"] != "# Changed" || s.jobs()[0].State != "complete" {
		t.Fatal("update not confirmed")
	}
	paused := false
	expected = 1
	if _, err = s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &paused, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.put("local", "a.md", "# After pause", 2); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if len(s.jobs()) != 1 || f.contents()["a.md"] != "# Changed" || s.mirrors()[0].State != "paused" {
		t.Fatal("paused mirror continued")
	}
	r, err := openStore(s.dir)
	if err != nil || r.db.Version != 19 || len(r.mirrors()) != 1 || r.mirrors()[0].Enabled {
		t.Fatal("configuration did not survive reopen", err)
	}
}
func TestMirrorKeepsConflictsAndDoesNotPublish(t *testing.T) {
	s, m, f := mirrorFixture(t)
	enabled := true
	expected := 0
	if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &enabled, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	remote := f.contents()
	remote["a.md"] = "# Remote change"
	f.advance(remote)
	m.Once(context.Background())
	if _, err := s.put("local", "a.md", "# Local change", 1); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	if s.mirrors()[0].State != "blocked" || s.mirrors()[0].Blocked != 1 || f.contents()["a.md"] != "# Remote change" || len(s.jobs()) != 0 {
		t.Fatal("divergence overwritten")
	}
	view := s.mirrors()
	if len(view[0].BlockedPaths) != 1 || view[0].BlockedPaths[0] != "a.md" {
		t.Fatal("blocked path missing")
	}
	view[0].BlockedPaths[0] = "changed"
	if s.mirrors()[0].BlockedPaths[0] != "a.md" {
		t.Fatal("mirror status exposed mutable state")
	}
	f.modify(func() { f.private = false })
	m.Once(context.Background())
	if s.mirrors()[0].State != "error" || f.writeCount() != 0 {
		t.Fatal("public destination accepted")
	}
}
func TestMirrorConfigurationGuardsAndOwnerAPI(t *testing.T) {
	s, m, _ := mirrorFixture(t)
	enabled := true
	expected := 0
	in := mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &enabled, ExpectedRevision: &expected}
	w := Wiki{Store: s, Token: "test-owner", Sync: m}
	body := `{"source_id":"local","destination_id":"handbook","enabled":true,"expected_revision":0}`
	if r := request(Wiki{Store: s, Token: "test-owner"}, "POST", "/api/v1/sources/mirrors", body, true); r.Code != 503 || len(s.mirrors()) != 0 {
		t.Fatal("mirror enabled without worker", r.Code)
	}
	if r := request(w, "GET", "/api/v1/sources/mirrors", "", false); r.Code != 404 {
		t.Fatal("mirror settings exposed", r.Code)
	}
	if r := request(w, "POST", "/api/v1/sources/mirrors", body, false); r.Code != 403 {
		t.Fatal("unauthorized mirror", r.Code)
	}
	if r := request(w, "POST", "/api/v1/sources/mirrors", body, true); r.Code != 200 {
		t.Fatal("owner mirror failed", r.Code)
	}
	if _, err := s.configureMirror(context.Background(), in); !errors.Is(err, ErrConflict) {
		t.Fatal("stale configuration accepted", err)
	}
	paused := false
	expected = 1
	if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &paused, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	old := 1
	if _, err := s.reconcileSourcesExpected(context.Background(), reconcileRequest{SourceID: "local", DestinationID: "handbook"}, &old); !errors.Is(err, ErrConflict) {
		t.Fatal("paused stale worker accepted", err)
	}
	if r := request(w, "GET", "/api/v1/sources/mirrors", "", true); r.Code != 200 || !strings.Contains(r.Body.String(), "paused") {
		t.Fatal("owner status unavailable", r.Code)
	}
}
func TestMirrorSQLiteSurvivesReopen(t *testing.T) {
	s, _, f := mirrorFixture(t)
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	expected := 0
	if _, err = s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &enabled, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil || len(s.mirrors()) != 1 || !s.mirrors()[0].Enabled {
		t.Fatal("mirror lost on reopen", err)
	}
	m := newSyncManager(s, f.client())
	m.Once(context.Background())
	if _, err = s.put("local", "a.md", "# Edited after reopen", 1); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil || s.mirrors()[0].State != "ready" || s.db.Articles["handbook/a.md"].ReconcileBase == nil {
		t.Fatal("background status not persisted", err)
	}
}

func TestMirrorPauseRetainsQueuedWrite(t *testing.T) {
	s, m, f := mirrorFixture(t)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	s.put("local", "a.md", "# Queued", 1)
	m.Once(context.Background())
	if len(s.jobs()) != 1 {
		t.Fatal("write not queued")
	}
	no := false
	revision = 1
	if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &no, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	s.put("local", "a.md", "# After pause", 2)
	m.Once(context.Background())
	m.Once(context.Background())
	if f.contents()["a.md"] != "# Queued" || len(s.jobs()) != 1 || s.jobs()[0].State != "complete" {
		t.Fatal("pause discarded queued write or created another")
	}
}

func TestMirrorMultipleBatchesEventuallyConverge(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	for i := 0; i < 22; i++ {
		name := fmt.Sprintf("p%02d.md", i)
		s.put("local", name, "# "+name, 0)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, true)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		m.Once(context.Background())
		if s.mirrors()[0].State == "ready" {
			break
		}
	}
	if s.mirrors()[0].State != "ready" || len(f.contents()) != 23 {
		t.Fatal("multiple batches did not converge", s.mirrors()[0].State, len(f.contents()))
	}
	for _, job := range s.jobs() {
		if job.State != "complete" {
			t.Fatal("unfinished job", job.State)
		}
	}
}

func TestMirrorThroughMCP(t *testing.T) {
	python := os.Getenv("VEDA_TEST_MCP_PYTHON")
	if python == "" {
		t.Skip("set VEDA_TEST_MCP_PYTHON to a Python with the MCP SDK")
	}
	s, m, _ := mirrorFixture(t)
	token := strings.Repeat("a", 64)
	key := filepath.Join(t.TempDir(), "owner.key")
	if err := os.WriteFile(key, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(Wiki{Store: s, Token: token, Sync: m})
	defer web.Close()
	adapter, err := filepath.Abs("../mcp/server.py")
	if err != nil {
		t.Fatal(err)
	}
	script := `import asyncio,json,sys
from mcp import ClientSession,StdioServerParameters
from mcp.client.stdio import stdio_client
async def main():
 p=StdioServerParameters(command=sys.executable,args=[sys.argv[1],"--url",sys.argv[2],"--key-file",sys.argv[3]])
 async with stdio_client(p) as (r,w):
  async with ClientSession(r,w) as s:
   await s.initialize()
   x=await s.call_tool("veda_configure_source_mirror",{"source_id":"local","destination_id":"handbook","enabled":True,"expected_revision":0})
   assert not x.isError and json.loads(x.content[0].text)["revision"]==1
   y=await s.call_tool("veda_source_mirrors",{})
   assert not y.isError and json.loads(y.content[0].text)["mirrors"][0]["enabled"]
asyncio.run(main())`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, python, "-c", script, adapter, web.URL, key).CombinedOutput(); err != nil {
		t.Fatalf("synthetic MCP failed: %v %s", err, out)
	}
	m.Once(context.Background())
	if s.mirrors()[0].State != "ready" || s.db.Articles["handbook/a.md"].ReconcileBase == nil {
		t.Fatal("MCP-configured mirror did not run")
	}
}

func TestOldBinaryRejectsMirrorStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V19_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	for _, sqlite := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sqlite"}[sqlite], func(t *testing.T) {
			s, _, _ := mirrorFixture(t)
			if sqlite {
				if err := migrateSQLite(s.dir); err != nil {
					t.Fatal(err)
				}
				var err error
				s, err = openStore(s.dir)
				if err != nil {
					t.Fatal(err)
				}
			}
			yes := true
			revision := 0
			if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
				t.Fatal(err)
			}
			name := "state.json"
			if sqlite {
				name = "state.sqlite"
			}
			file := filepath.Join(s.dir, name)
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || ctx.Err() != nil || (!strings.Contains(string(out), "unsupported store") && !strings.Contains(string(out), "unknown SQLite record category")) {
				t.Fatalf("old binary did not reject mirror store: %s", out)
			}
			after, err := os.ReadFile(file)
			if err != nil || string(after) != string(before) {
				t.Fatal("old binary changed mirror store")
			}
		})
	}
}

func TestMirrorAdminSessionBoundaries(t *testing.T) {
	s, m, _ := mirrorFixture(t)
	w := Wiki{Store: s, Token: "owner", AdminSessions: &readerSessions{}, Sync: m}
	call := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.Host = "localhost:8080"
		r.RemoteAddr = "127.0.0.1:12345"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	login := call("POST", "/api/admin/session/login", `{"key":"owner"}`, "http://localhost:8080", nil)
	if login.Code != 200 {
		t.Fatal("login", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	body := `{"source_id":"local","destination_id":"handbook","enabled":true,"expected_revision":0}`
	if r := call("GET", "/api/admin/github/mirrors", "", "", nil); r.Code != 404 {
		t.Fatal("anonymous mirror access", r.Code)
	}
	if r := call("POST", "/api/admin/github/mirrors", body, "http://evil.example", cookie); r.Code != 403 {
		t.Fatal("cross-origin mirror access", r.Code)
	}
	if r := call("POST", "/api/admin/github/mirrors", body, "http://localhost:8080", cookie); r.Code != 200 {
		t.Fatal("admin mirror rejected", r.Code)
	}
	if r := call("GET", "/api/admin/github/mirrors", "", "", cookie); r.Code != 200 || !strings.Contains(r.Body.String(), "handbook") {
		t.Fatal("mirror status unavailable", r.Code)
	}
	if r := call("POST", "/api/v1/articles", `{}`, "http://localhost:8080", cookie); r.Code != 403 {
		t.Fatal("admin session gained article write", r.Code)
	}
}

func TestMirrorSQLiteManyMatchingArticles(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	remote := map[string]string{"veda.yaml": metadataFixture}
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("p%03d.md", i)
		text := "# " + name
		s.put("local", name, text, 0)
		remote[name] = text
	}
	f := newFakeGitHub(t, remote)
	f.modify(func() { f.private = true })
	connectFixture(t, s, f, true)
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	revision := 0
	if _, err = s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	m := newSyncManager(s, f.client())
	for i := 0; i < 6; i++ {
		m.Once(context.Background())
		if s.mirrors()[0].State == "ready" {
			break
		}
	}
	if s.mirrors()[0].State != "ready" {
		t.Fatal("matching batch did not converge", s.mirrors()[0].State)
	}
	reopened, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, a := range reopened.db.Articles {
		if a.SourceID == "handbook" && a.ReconcileBase != nil {
			count++
		}
	}
	if count != 100 || len(s.jobs()) != 0 {
		t.Fatal("baseline batch not durable", count)
	}
}

func TestMirrorWorkerRunsOnIntervalAndStops(t *testing.T) {
	s, m, f := mirrorFixture(t)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx, 20*time.Millisecond) }()
	defer func() { cancel(); <-done }()
	await := func(check func() bool) {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			if check() {
				return
			}
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatal("background worker did not converge")
			}
		}
	}
	await(func() bool { return s.mirrors()[0].State == "ready" })
	if _, err := s.put("local", "a.md", "# Periodic", 1); err != nil {
		t.Fatal(err)
	}
	await(func() bool { return f.contents()["a.md"] == "# Periodic" && s.mirrors()[0].State == "ready" })
}

func TestMirrorCancelledWhileWaitingDoesNotChangeConfiguration(t *testing.T) {
	s, _, _ := mirrorFixture(t)
	yes := true
	revision := 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.mu.Lock()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := s.configureMirror(ctx, mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision})
		done <- err
	}()
	<-started
	cancel()
	s.mu.Unlock()
	err := <-done
	if !errors.Is(err, context.Canceled) || len(s.mirrors()) != 0 {
		t.Fatal("cancelled configuration committed", err)
	}
}

func TestLocalImportWakesEnabledMirrorOnlyForChanges(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.SourceID = "local"
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Initial"})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, true)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.wake:
	default:
	}
	current := s.db.Sources["local"].ImportGeneration
	b.Create = false
	b.Complete = false
	b.ExpectedImport = &current
	b.Files["README.md"] = "# Changed"
	encoded, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(encoded))
	r = r.WithContext(context.WithValue(r.Context(), localRootKey{}, true))
	out := httptest.NewRecorder()
	Wiki{Store: s, ReaderLocal: true, LocalImportSource: "local", Sync: m}.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("local import failed", out.Code)
	}
	select {
	case <-m.wake:
	default:
		t.Fatal("mirror not awakened")
	}
	current = s.db.Sources["local"].ImportGeneration
	b.ExpectedImport = &current
	encoded, _ = json.Marshal(b)
	r = httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(encoded))
	r = r.WithContext(context.WithValue(r.Context(), localRootKey{}, true))
	out = httptest.NewRecorder()
	Wiki{Store: s, ReaderLocal: true, LocalImportSource: "local", Sync: m}.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("local replay failed", out.Code)
	}
	select {
	case <-m.wake:
		t.Fatal("unchanged import awakened mirror")
	default:
	}
	current = s.db.Sources["local"].ImportGeneration
	b.ExpectedImport = &current
	b.Assets = map[string][]byte{"image.png": smallPNG(t)}
	encoded, _ = json.Marshal(b)
	r = httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(encoded))
	r = r.WithContext(context.WithValue(r.Context(), localRootKey{}, true))
	out = httptest.NewRecorder()
	Wiki{Store: s, ReaderLocal: true, LocalImportSource: "local", Sync: m}.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("asset-only import failed", out.Code)
	}
	select {
	case <-m.wake:
	default:
		t.Fatal("asset-only import did not wake mirror")
	}
	no := false
	revision = 1
	if _, err = s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &no, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	current = s.db.Sources["local"].ImportGeneration
	b.ExpectedImport = &current
	b.Files["README.md"] = "# Changed while paused"
	encoded, _ = json.Marshal(b)
	r = httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(encoded))
	r = r.WithContext(context.WithValue(r.Context(), localRootKey{}, true))
	out = httptest.NewRecorder()
	Wiki{Store: s, ReaderLocal: true, LocalImportSource: "local", Sync: m}.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("paused local import failed", out.Code)
	}
	select {
	case <-m.wake:
		t.Fatal("paused mirror awakened")
	default:
	}
}

func TestMirrorQueuedPrivateWriteNeverPublishesAfterVisibilityChange(t *testing.T) {
	s, m, f := mirrorFixture(t)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	m.Once(context.Background())
	s.put("local", "a.md", "# Private local update", 1)
	m.Once(context.Background())
	if len(s.jobs()) != 1 || s.jobs()[0].Visibility != "private" {
		t.Fatal("private job missing")
	}
	f.modify(func() { f.private = false })
	m.Once(context.Background())
	if f.writeCount() != 0 || f.contents()["a.md"] != "# Same" || s.mirrors()[0].State != "error" {
		t.Fatal("private mirror write reached public repository")
	}
}

func TestMirrorConnectedSourcesAndCycleGuard(t *testing.T) {
	s, m, first, second := connectedMoveFixtures(t)
	files := second.contents()
	files["README.md"] = "# Source"
	second.advance(files)
	m.Once(context.Background())
	enabled, revision := true, 0
	in := mirrorRequest{SourceID: "one", DestinationID: "two", Enabled: &enabled, ExpectedRevision: &revision}
	if _, err := s.configureMirror(context.Background(), in); err != nil {
		t.Fatal("connected source rejected", err)
	}
	m.Once(context.Background())
	files = first.contents()
	files["README.md"] = "# Changed upstream"
	first.advance(files)
	for i := 0; i < 4; i++ {
		m.Once(context.Background())
	}
	if second.contents()["README.md"] != "# Changed upstream" {
		t.Fatal("connected source change not confirmed")
	}
	if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "two", DestinationID: "one", Enabled: &enabled, ExpectedRevision: &revision}); !errors.Is(err, ErrConflict) {
		t.Fatal("cycle accepted", err)
	}
	first.modify(func() { first.private = false })
	m.Once(context.Background())
	if s.mirrors()[0].State != "error" {
		t.Fatal("source visibility change not rejected")
	}
}

func TestConnectedMirrorRejectsUnverifiedAndPartialSource(t *testing.T) {
	for _, condition := range []string{"paused", "unverified", "partial", "same-repository"} {
		t.Run(condition, func(t *testing.T) {
			s, _, _, _ := connectedMoveFixtures(t)
			s.mu.Lock()
			src := s.db.Sources["one"]
			c := s.db.Connections[src.ConnectionID]
			switch condition {
			case "paused":
				c.Paused = true
			case "unverified":
				c.Verification = "unknown"
			case "partial":
				src.ImportPartial = true
			case "same-repository":
				c.RepositoryID = s.db.Connections["two"].RepositoryID
				c.Branch = s.db.Connections["two"].Branch
			}
			s.db.Sources["one"] = src
			s.db.Connections[src.ConnectionID] = c
			s.mu.Unlock()
			enabled, revision := true, 0
			if _, err := s.configureMirror(context.Background(), mirrorRequest{SourceID: "one", DestinationID: "two", Enabled: &enabled, ExpectedRevision: &revision}); !errors.Is(err, ErrConflict) {
				t.Fatal("unsafe source accepted", err)
			}
			if _, err := s.reconcileSources(context.Background(), reconcileRequest{SourceID: "one", DestinationID: "two"}); !errors.Is(err, ErrConflict) {
				t.Fatal("unsafe source reconciled", err)
			}
		})
	}
}

func TestMirrorCycleIncludesLongerChains(t *testing.T) {
	db := Database{SourceMirrors: map[string]SourceMirror{
		"b": {SourceID: "a", Enabled: true},
		"c": {SourceID: "b", Enabled: true},
	}}
	if !mirrorWouldCycle(db, "c", "a") || mirrorWouldCycle(db, "a", "c") {
		t.Fatal("invalid cycle detection")
	}
}

func TestConnectedReconcileBlocksUnobservedLinkedArticle(t *testing.T) {
	s, m, first, _ := connectedMoveFixtures(t)
	files := first.contents()
	files["new.md"] = "[Linked](linked.md)"
	files["linked.md"] = "# Linked"
	first.advance(files)
	m.Once(context.Background())
	s.mu.Lock()
	linked := s.db.Articles["one/linked.md"]
	linked.UpstreamHash = ""
	s.db.Articles["one/linked.md"] = linked
	s.mu.Unlock()
	r, err := s.reconcileSources(context.Background(), reconcileRequest{SourceID: "one", DestinationID: "two"})
	if err != nil || !containsPath(r.Blocked, "new.md") || !containsPath(r.Blocked, "linked.md") {
		t.Fatal("unobserved linked revision accepted", r, err)
	}
	if _, ok := s.db.Articles["two/new.md"]; ok {
		t.Fatal("linked graph copied despite unobserved revision")
	}
}
