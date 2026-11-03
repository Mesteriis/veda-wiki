package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReconcileSourcesPreservesIndependentChanges(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	for p, text := range map[string]string{"same.md": "# Same", "different.md": "# Local", "new.md": "# New"} {
		if _, err := s.put("local", p, text, 0); err != nil {
			t.Fatal(err)
		}
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "same.md": "# Same", "different.md": "# Remote", "remote-only.md": "# Remote only"})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, true)
	in := reconcileRequest{SourceID: "local", DestinationID: "handbook", Limit: 50}
	r, err := s.reconcileSources(context.Background(), in)
	if err != nil || r.Baselines != 1 || r.Created != 1 || r.Updated != 0 || len(r.Blocked) != 1 || r.Blocked[0] != "different.md" {
		t.Fatalf("unexpected initial result %+v %v", r, err)
	}
	if s.db.Version != 18 || s.db.Articles["handbook/remote-only.md"].Deleted || f.writeCount() != 0 {
		t.Fatal("unsafe initial reconciliation")
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.contents()["new.md"] != "# New" {
		t.Fatal("new article not synchronized")
	}
	if _, err = s.put("local", "same.md", "# Local updated", 1); err != nil {
		t.Fatal(err)
	}
	r, err = s.reconcileSources(context.Background(), in)
	if err != nil || r.Updated != 1 {
		t.Fatal("safe source update not queued", r, err)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.contents()["same.md"] != "# Local updated" {
		t.Fatal("source update not confirmed")
	}
	contents := f.contents()
	contents["same.md"] = "# Independent remote"
	f.advance(contents)
	m.Once(context.Background())
	if _, err = s.put("local", "same.md", "# Independent local", 2); err != nil {
		t.Fatal(err)
	}
	r, err = s.reconcileSources(context.Background(), in)
	if err != nil || !containsPath(r.Blocked, "same.md") || s.db.Articles["handbook/same.md"].Versions[len(s.db.Articles["handbook/same.md"].Versions)-1].Text != "# Independent remote" {
		t.Fatal("divergence overwritten", r, err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Version != 18 || reopened.db.Articles["handbook/same.md"].ReconcileBase == nil {
		t.Fatal("baseline lost after reopen", err)
	}
}
func containsPath(values []string, p string) bool {
	for _, v := range values {
		if v == p {
			return true
		}
	}
	return false
}

func TestReconcileRequiresPrivateWritableVerifiedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# A", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture})
	m := connectFixture(t, s, f, false)
	in := reconcileRequest{SourceID: "local", DestinationID: "handbook"}
	if _, err := s.reconcileSources(context.Background(), in); !errors.Is(err, ErrConflict) {
		t.Fatal("public destination accepted", err)
	}
	f.modify(func() { f.private = true })
	_ = m
	w := Wiki{Store: s, Token: "test-owner", Sync: m}
	body := `{"source_id":"local","destination_id":"handbook"}`
	if r := request(w, "POST", "/api/v1/sources/reconcile", body, false); r.Code != 403 {
		t.Fatal("unauthorized reconciliation", r.Code)
	}
	if r := request(w, "POST", "/api/v1/sources/reconcile", body, true); r.Code != 409 {
		t.Fatal("public API reconciliation accepted", r.Code)
	}
}

func TestReconcileLinkedGraphAndPrivateBaseline(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "[B](b.md)", 0)
	s.put("local", "b.md", "[A](a.md)", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, true)
	r, err := s.reconcileSources(context.Background(), reconcileRequest{SourceID: "local", DestinationID: "handbook", Limit: 10})
	if err != nil || r.Created != 2 {
		t.Fatal("linked graph not queued", r, err)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.contents()["a.md"] != "[B](b.md)" || f.contents()["b.md"] != "[A](a.md)" {
		t.Fatal("linked graph not confirmed")
	}
	w := Wiki{Store: s, Token: "test-owner", ReaderToken: "test-reader"}
	r1 := request(w, "GET", "/api/v1/articles?source_id=handbook&path=a.md", "", false)
	if strings.Contains(r1.Body.String(), "reconcile_base") {
		t.Fatal("baseline leaked to public")
	}
	scoped, ok, err := s.scopedViewLocked([]string{"handbook"})
	if err != nil || !ok || scoped.db.Articles["handbook/a.md"].ReconcileBase != nil {
		t.Fatal("baseline leaked to scoped reader", err)
	}
}

func TestReconcileBoundedAndResumable(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	for _, p := range []string{"a.md", "b.md", "c.md"} {
		if _, err := s.put("local", p, "# Same "+p, 0); err != nil {
			t.Fatal(err)
		}
	}
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same a.md", "b.md": "# Same b.md", "c.md": "# Same c.md"})
	f.modify(func() { f.private = true })
	connectFixture(t, s, f, true)
	in := reconcileRequest{SourceID: "local", DestinationID: "handbook", Limit: 1}
	for i := 0; i < 3; i++ {
		r, err := s.reconcileSources(context.Background(), in)
		if err != nil || r.Baselines != 1 || r.More != (i < 2) {
			t.Fatal("bounded pass failed", r, err)
		}
	}
	r, err := s.reconcileSources(context.Background(), in)
	if err != nil || r.Unchanged != 3 || r.More {
		t.Fatal("replay not idempotent", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.reconcileSources(ctx, in); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}

func TestReconcileSQLiteSurvivesReopen(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.modify(func() { f.private = true })
	connectFixture(t, s, f, true)
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.reconcileSources(context.Background(), reconcileRequest{SourceID: "local", DestinationID: "handbook"})
	if err != nil || r.Baselines != 1 {
		t.Fatal("baseline failed", r, err)
	}
	reopened, err := openStore(s.dir)
	if err != nil || reopened.db.Version != 18 || reopened.db.Articles["handbook/a.md"].ReconcileBase == nil {
		t.Fatal("sqlite baseline lost", err)
	}
}

func TestReconcileSkipsSecretLikeAndMissingDependencies(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "secret.md", "password=fixture-only-secret", 0)
	s.put("local", "broken.md", "[Missing](missing.md)", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture})
	f.modify(func() { f.private = true })
	connectFixture(t, s, f, true)
	r, err := s.reconcileSources(context.Background(), reconcileRequest{SourceID: "local", DestinationID: "handbook"})
	if err != nil || len(r.Blocked) != 2 || len(s.jobs()) != 0 || len(s.db.Articles) != 2 {
		t.Fatal("unsafe data queued", r, err)
	}
}

func TestOldBinaryRejectsReconciliationStore(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V18_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.modify(func() { f.private = true })
	connectFixture(t, s, f, true)
	if _, err := s.reconcileSources(context.Background(), reconcileRequest{SourceID: "local", DestinationID: "handbook"}); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(s.dir, "state.json")
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(out), "unsupported store") {
		t.Fatal("old binary did not reject v18")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(after) != string(before) {
		t.Fatal("old binary changed v18 state")
	}
}

func TestReconcileThroughMCP(t *testing.T) {
	python := os.Getenv("VEDA_TEST_MCP_PYTHON")
	if python == "" {
		t.Skip("set VEDA_TEST_MCP_PYTHON to a Python with the MCP SDK")
	}
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.modify(func() { f.private = true })
	m := connectFixture(t, s, f, true)
	token := strings.Repeat("a", 64)
	key := filepath.Join(t.TempDir(), "owner.key")
	if err := os.WriteFile(key, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(Wiki{Store: s, Token: token, Sync: m})
	defer web.Close()
	adapter, err := filepath.Abs("../../mcp/server.py")
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
   x=await s.call_tool("veda_reconcile_sources",{"source_id":"local","destination_id":"handbook"})
   assert not x.isError and json.loads(x.content[0].text)["baselines"]==1
asyncio.run(main())`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", script, adapter, web.URL, key)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic MCP failed: %v %s", err, out)
	}
	if s.db.Version != 18 || s.db.Articles["handbook/a.md"].ReconcileBase == nil {
		t.Fatal("MCP did not persist baseline")
	}
}

func TestReconcileEqualArticleQueuesMissingImageAndBlocksDifferentImage(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "different"}[different], func(t *testing.T) {
			s := testStore(t)
			source(t, s, "local", "private")
			ref := fixtureAsset(t, s, "local", "img/icon.png")
			text := "# Same\n![icon](img/icon.png)"
			s.put("local", "a.md", text, 0)
			files := map[string]string{"veda.yaml": metadataFixture, "a.md": text}
			if different {
				files["img/icon.png"] = string(append(smallPNG(t), 1))
			}
			f := newFakeGitHub(t, files)
			f.modify(func() { f.private = true })
			m := connectFixture(t, s, f, true)
			r, err := s.reconcileSources(context.Background(), reconcileRequest{SourceID: "local", DestinationID: "handbook"})
			if err != nil {
				t.Fatal(err)
			}
			if different {
				if !containsPath(r.Blocked, "a.md") || len(s.jobs()) != 0 {
					t.Fatal("different image was overwritten", r)
				}
				return
			}
			if r.Updated != 1 || len(s.jobs()) != 1 || s.jobs()[0].Assets["img/icon.png"] != ref {
				t.Fatal("missing image not queued", r)
			}
			m.Once(context.Background())
			m.Once(context.Background())
			if digest([]byte(f.contents()["img/icon.png"])) != ref.SHA || s.jobs()[0].State != "complete" {
				t.Fatal("missing image not confirmed")
			}
		})
	}
}

func TestReconcileRejectsInvalidStoredBaseline(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	source(t, s, "target", "private")
	s.put("local", "a.md", "# A", 0)
	s.put("target", "a.md", "# A", 0)
	b := s.db.Articles["target/a.md"]
	b.ReconcileBase = &ReconcileBase{SourceID: "local", Path: "a.md", Revision: 1, SHA: "invalid"}
	db := s.clone()
	db.Version = 18
	db.Articles["target/a.md"] = b
	if err := s.commit(db); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(s.dir); err == nil {
		t.Fatal("invalid baseline accepted on reopen")
	}
}

func TestOldBinaryRejectsReconciliationSQLite(t *testing.T) {
	old := os.Getenv("VEDA_OLD_V18_BINARY")
	if old == "" {
		t.Skip("old binary not provided")
	}
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.modify(func() { f.private = true })
	connectFixture(t, s, f, true)
	if err := migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.reconcileSources(context.Background(), reconcileRequest{SourceID: "local", DestinationID: "handbook"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(s.dir, "state.sqlite")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, old, "--data", s.dir, "--listen", "127.0.0.1:0", "--public-listen", "127.0.0.1:0").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil || !strings.Contains(string(out), "unsupported store") {
		t.Fatal("old binary did not reject SQLite v18")
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(before) {
		t.Fatal("old binary changed SQLite v18 state")
	}
}

func TestConcurrentReconcileDoesNotDuplicateWrites(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "same.md", "# Same", 0)
	s.put("local", "new.md", "# New", 0)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "same.md": "# Same"})
	f.modify(func() { f.private = true })
	connectFixture(t, s, f, true)
	type outcome struct {
		result reconcileResult
		err    error
	}
	out := make(chan outcome, 8)
	for i := 0; i < 8; i++ {
		go func() {
			r, err := s.reconcileSources(context.Background(), reconcileRequest{SourceID: "local", DestinationID: "handbook"})
			out <- outcome{r, err}
		}()
	}
	baselines, created := 0, 0
	for i := 0; i < 8; i++ {
		r := <-out
		if r.err != nil {
			t.Fatal(r.err)
		}
		baselines += r.result.Baselines
		created += r.result.Created
	}
	if baselines != 1 || created != 1 || len(s.jobs()) != 1 || len(s.db.Articles["handbook/new.md"].Versions) != 1 {
		t.Fatal("concurrent reconciliations duplicated writes")
	}
}
