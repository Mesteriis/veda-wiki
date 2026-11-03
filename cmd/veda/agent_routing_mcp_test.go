package main

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Exercise source-qualified routing with the MCP SDK after SQLite restart.
func TestAgentRoutesVPSRequestAcrossSources(t *testing.T) {
	python := os.Getenv("VEDA_TEST_MCP_PYTHON")
	if python == "" {
		t.Skip("set VEDA_TEST_MCP_PYTHON to a Python with the MCP SDK")
	}
	s := testStore(t)
	for _, id := range []string{"vps", "homelab"} {
		source(t, s, id, "private")
		for p, text := range map[string]string{
			"AGENTS.md":         "# Root instructions for " + id,
			"ops/AGENTS.md":     "# Operations instructions for " + id,
			"ops/containers.md": "# LXC VPS containers\n\nSynthetic " + id + " instructions.",
		} {
			if _, err := s.put(id, p, text, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	both, err := s.auth().CreateAccessKey("both", []string{"vps", "homelab"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := s.auth().CreateAccessKey("vps-only", []string{"vps"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	s.Lock()
	s.DB().Moves = map[string]Move{"fixture-move": {State: "deleting_source", SourceID: "vps", SourcePath: "ops/containers.md", DestinationID: "homelab", DestinationPath: "ops/other.md"}}
	s.DB().Outbox = map[string]SyncJob{"fixture-bundle": {State: "pending", SourceID: "homelab", Path: "ops/root.md", Bundle: map[string]string{"ops/containers.md": "fixture-hash"}}}
	s.Unlock()
	server := httptest.NewServer(Wiki{Store: s, Token: "owner"})
	defer server.Close()
	keys := t.TempDir()
	for name, value := range map[string]string{"both": both, "restricted": restricted} {
		if err := os.WriteFile(filepath.Join(keys, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
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
   found=await call('veda_find',{'query':'создай контейнер на моём VSP'})
   assert {(x['source_id'],x['path']) for x in found['results']}=={(x,'ops/containers.md') for x in expected}
   for source in expected:
    context=await call('veda_context',{'source_id':source,'path':'ops/containers.md'})
    assert [x['path'] for x in context['instructions']]==['AGENTS.md','ops/AGENTS.md']
    assert all(x['source_id']==source and ('for '+source) in x['text'] for x in context['instructions'])
    article=await call('veda_read',{'source_id':source,'path':'ops/containers.md'})
    assert ('Synthetic '+source) in article['text']
    proposal=await call('veda_prepare_update',{'source_id':source,'path':'ops/containers.md','expected_revision':article['revision'],'text':'# Proposed synthetic update'})
    assert proposal['saved'] is False and proposal['base_sha256']==article['sha256']
    assert proposal['write_blocked_by_move']==(source=='vps')
    assert proposal['write_blocked_by_bundle']==(source=='homelab')
    if source=='vps':
     assert 'move blocks writes' in proposal['note'] and 'homelab' not in json.dumps(proposal)
    else:
     assert 'bundle blocks writes' in proposal['note'] and 'ops/root.md' not in json.dumps(proposal)
    assert (await call('veda_read',{'source_id':source,'path':'ops/containers.md'}))['text']==article['text']
   if expected=={'vps'}:
    result=await s.call_tool('veda_context',{'source_id':'homelab','path':'ops/containers.md'})
    assert result.isError and 'Operations instructions for homelab' not in str(result)
async def main():
 await run(sys.argv[3],{'vps','homelab'})
 await run(sys.argv[4],{'vps'})
asyncio.run(main())`
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", script, adapter, server.URL, filepath.Join(keys, "both"), filepath.Join(keys, "restricted"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("agent routing integration failed: %v\n%s", err, output)
	}
}
