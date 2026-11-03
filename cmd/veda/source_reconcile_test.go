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

	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func TestReconcileRequiresPrivateWritableVerifiedDestination(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# A", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture})
	m := connectFixture(t, s, f, false)
	in := reconcileRequest{SourceID: "local", DestinationID: "handbook"}
	if _, err := s.reconcileSources(context.Background(), in); !errors.Is(err, ErrConflict) {
		t.Fatal("public destination accepted", err)
	}
	f.Modify(func() { f.Private = true })
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	r, err := s.reconcileSources(context.Background(), reconcileRequest{SourceID: "local", DestinationID: "handbook", Limit: 10})
	if err != nil || r.Created != 2 {
		t.Fatal("linked graph not queued", r, err)
	}
	m.Once(context.Background())
	m.Once(context.Background())
	if f.Contents()["a.md"] != "[B](b.md)" || f.Contents()["b.md"] != "[A](a.md)" {
		t.Fatal("linked graph not confirmed")
	}
	w := Wiki{Store: s, Token: "test-owner", ReaderToken: "test-reader"}
	r1 := request(w, "GET", "/api/v1/articles?source_id=handbook&path=a.md", "", false)
	if strings.Contains(r1.Body.String(), "reconcile_base") {
		t.Fatal("baseline leaked to public")
	}
	scoped, ok, err := s.ScopedWhere(func(*Database) []string { return []string{"handbook"} })
	if err != nil || !ok || scoped.DB().Articles["handbook/a.md"].ReconcileBase != nil {
		t.Fatal("baseline leaked to scoped reader", err)
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
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.Modify(func() { f.Private = true })
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
	if s.DB().Version != 18 || s.DB().Articles["handbook/a.md"].ReconcileBase == nil {
		t.Fatal("MCP did not persist baseline")
	}
}
