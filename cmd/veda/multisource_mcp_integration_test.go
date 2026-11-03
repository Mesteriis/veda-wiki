package main

import (
	"context"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
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
	one := githubtest.New(t, map[string]string{"docs/guide.md": "# Shared public\nPublic fixture"})
	two := githubtest.New(t, map[string]string{"docs/guide.md": "# Shared private\nPrivate fixture"})
	two.Modify(func() { two.Name = "org/second"; two.RepoID = 42; two.Private = true })
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/org/second") {
			two.Serve(w, r)
		} else {
			one.Serve(w, r)
		}
	}))
	defer gateway.Close()
	client := github.NewForTest(gateway.URL, gateway.Client())
	manager := newSyncManager(s, client)
	if _, err := s.saveAccount(context.Background(), client, "operator", github.Secret{Kind: "token", Token: githubtest.Access}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.connect(context.Background(), connectRequest{ID: "one", Repository: "org/handbook"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.connect(context.Background(), connectRequest{ID: "two", Repository: "org/second", AccountID: "operator", Write: true}); err != nil {
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
	if _, err := s.put("two", "docs/guide.md", "# Shared private\nLocal pending fixture", 1); err != nil {
		t.Fatal(err)
	}
	two.Modify(func() { two.LoseResponse = true })
	manager.Once(context.Background())
	if jobs := s.jobs(); len(jobs) != 1 || jobs[0].PreparedCommit == "" {
		t.Fatal("fixture did not create an unconfirmed prepared write")
	}
	two.Advance(map[string]string{"docs/guide.md": "# Shared private\nDifferent upstream fixture"})
	if err := s.retryConnection("two"); err != nil {
		t.Fatal(err)
	}
	manager.Once(context.Background())
	if len(s.conflicts()) != 1 {
		t.Fatal("fixture did not create an import conflict")
	}
	s, err = openStore(s.dir)
	if err != nil {
		t.Fatal(err)
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
	adapter, err := filepath.Abs("../../mcp/server.py")
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
    assert article['text']==('# Shared public\nPublic fixture' if source=='one' else '# Shared private\nLocal pending fixture')
    context=await call('veda_context',{'source_id':source,'path':'docs/guide.md'})
    assert context['source_status']['last_import_observed_at'] is None
    assert context['source_status']['sync_state']==('ready' if source=='one' else 'conflict')
    assert context['source_status']['sync_verification']=='verified'
    assert context['source_status']['unconfirmed_write_jobs']==(0 if source=='one' else 1)
    assert context['source_status']['prepared_unconfirmed_write_jobs']==(0 if source=='one' else 1)
    assert 'prepared_commit' not in json.dumps(context)
    proposal=await call('veda_prepare_update',{'source_id':source,'path':'docs/guide.md','expected_revision':article['revision'],'text':'# Proposed'})
    assert proposal['saved'] is False and proposal['base_sha256']==article['sha256']
    assert proposal['conflict_resolution_blocked_by_write']==(source=='two')
    if source=='two':
     assert proposal['unresolved_conflict'] and 'blocks conflict resolution' in proposal['note']
    assert proposal['source_status']['unconfirmed_write_jobs']==(0 if source=='one' else 1)
    assert proposal['source_status']['prepared_unconfirmed_write_jobs']==(0 if source=='one' else 1)
   if expected=={'one'}:
    denied=await s.call_tool('veda_read',{'source_id':'two','path':'docs/guide.md'})
    assert denied.isError and 'Local pending fixture' not in str(denied)
    denied_context=await s.call_tool('veda_context',{'source_id':'two','path':'docs/guide.md'})
    assert denied_context.isError and 'prepared_unconfirmed_write_jobs' not in str(denied_context)
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
