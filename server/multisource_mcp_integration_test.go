package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Optional integration exercise using the real MCP SDK, two fake GitHub
// repositories, source-scoped keys and a restarted SQLite store.
func TestMultiSourceMCPAfterSQLiteRestart(t *testing.T) {
	python := os.Getenv("VEDA_TEST_MCP_PYTHON")
	if python == "" {
		t.Skip("set VEDA_TEST_MCP_PYTHON to a Python with the MCP SDK")
	}
	s := testStore(t)
	one := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Shared public\nPublic fixture"})
	two := newFakeGitHub(t, map[string]string{"docs/guide.md": "# Shared private\nPrivate fixture"})
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
	manager := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", GitHubSecret{Kind: "token", Token: fixtureAccess}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.connect(context.Background(), connectRequest{ID: "one", Repository: "org/handbook"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.connect(context.Background(), connectRequest{ID: "two", Repository: "org/second", AccountID: "operator"}); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	both, err := s.createAccessKey("both", []string{"one", "two"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	limited, err := s.createAccessKey("oneonly", []string{"one"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateSQLite(s.dir); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	manager = newSyncManager(s, client)
	manager.Once(context.Background())
	if len(s.snapshot(false).Pages) != 2 || len(s.snapshot(true).Pages) != 1 {
		t.Fatal("merged visibility incorrect")
	}
	server := httptest.NewServer(Wiki{Store: s, Token: "owner"})
	defer server.Close()
	keyDir := t.TempDir()
	bothPath := filepath.Join(keyDir, "both.key")
	limitedPath := filepath.Join(keyDir, "limited.key")
	if err = os.WriteFile(bothPath, []byte(both), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(limitedPath, []byte(limited), 0600); err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs("../mcp/server.py")
	if err != nil {
		t.Fatal(err)
	}
	script := `import asyncio,json,sys
from mcp import ClientSession,StdioServerParameters
from mcp.client.stdio import stdio_client
async def run(key, expected):
 p=StdioServerParameters(command=sys.executable,args=[sys.argv[1],'--url',sys.argv[2],'--reader-key-file',key])
 async with stdio_client(p) as (r,w):
  async with ClientSession(r,w) as s:
   await s.initialize()
   async def call(name,args):
    result=await s.call_tool(name,args)
    if result.isError:raise RuntimeError('MCP tool failed: '+name)
    return json.loads(result.content[0].text)
   sources=await call('veda_sources',{})
   assert {x['id'] for x in sources['sources']}==expected
   found=await call('veda_find',{'query':'shared'})
   assert {(x['source_id'],x['path']) for x in found['results']}=={(x,'docs/guide.md') for x in expected}
   for source in expected:
    article=await call('veda_read',{'source_id':source,'path':'docs/guide.md'})
    assert article['text']==('# Shared public\nPublic fixture' if source=='one' else '# Shared private\nPrivate fixture')
    context=await call('veda_context',{'source_id':source,'path':'docs/guide.md'})
    assert context['source_status']['last_import_observed_at'] is not None
    proposal=await call('veda_prepare_update',{'source_id':source,'path':'docs/guide.md','expected_revision':article['revision'],'text':'# Proposed'})
    assert proposal['saved'] is False and proposal['base_sha256']==article['sha256']
   if expected=={'one'}:
    denied=await s.call_tool('veda_read',{'source_id':'two','path':'docs/guide.md'})
    assert denied.isError and 'Private fixture' not in str(denied)
async def main():
 await run(sys.argv[3],{'one','two'})
 await run(sys.argv[4],{'one'})
asyncio.run(main())`
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", script, adapter, server.URL, bothPath, limitedPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("MCP multisource integration failed: %v\n%s", err, output)
	}
}
