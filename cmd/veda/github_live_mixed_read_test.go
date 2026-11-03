package main

import (
	"context"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Read-only live acceptance. gh owns its credential; VEDA receives no token.
// The fixture branch contains only synthetic data, and both repositories must
// be public. This does not test VEDA's saved GitHub credentials or writes.
func TestLiveGitHubMixedSourcesReadOnly(t *testing.T) {
	if os.Getenv("VEDA_LIVE_GITHUB_MIXED") != "1" {
		t.Skip("live acceptance not enabled")
	}
	python := os.Getenv("VEDA_TEST_MCP_PYTHON")
	if python == "" {
		t.Fatal("MCP SDK Python is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	login, err := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login").Output()
	if err != nil || !strings.EqualFold(strings.TrimSpace(string(login)), "Mesteriis") {
		t.Fatal("Mesteriis account required")
	}
	repos := []string{"Mesteriis/veda-wiki", "Mesteriis/mac-tower"}
	for _, repo := range repos {
		out, err := exec.CommandContext(ctx, "gh", "api", "repos/"+repo, "--jq", ".private").Output()
		if err != nil || strings.TrimSpace(string(out)) != "false" {
			t.Fatal("fixture repository is not verified public")
		}
	}
	var failWiki atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "read only", http.StatusForbidden)
			return
		}
		route := strings.TrimPrefix(r.URL.RequestURI(), "/")
		allowed := false
		for _, repo := range repos {
			if route == "repos/"+repo || strings.HasPrefix(route, "repos/"+repo+"/") {
				allowed = true
			}
		}
		if !allowed {
			http.Error(w, "route denied", http.StatusForbidden)
			return
		}
		if failWiki.Load() && (route == "repos/"+repos[0] || strings.HasPrefix(route, "repos/"+repos[0]+"/")) {
			http.Error(w, "synthetic outage", http.StatusServiceUnavailable)
			return
		}
		body, err := exec.CommandContext(ctx, "gh", "api", route, "--method", "GET").Output()
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer proxy.Close()
	s := testStore(t)
	m := newSyncManager(s, github.NewForTest(proxy.URL, proxy.Client()))
	for _, request := range []connectRequest{
		{ID: "wiki", Repository: repos[0], Branch: "feature/veda-live-source-20261101-221944"},
		{ID: "program", Repository: repos[1]},
	} {
		if _, err := m.connect(ctx, request); err != nil {
			t.Fatal("public connection failed", err)
		}
	}
	m.Once(ctx)
	for _, c := range s.connections() {
		if c.State != "ready" {
			t.Fatalf("import did not finish: %s %s", c.ID, c.LastError)
		}
	}
	sources := s.sources(true)
	if len(sources) != 2 {
		t.Fatal("public sources unavailable")
	}
	kinds := map[string]string{}
	for _, source := range sources {
		kinds[source.ID] = source.Kind
		if source.ImportPartial {
			t.Fatal("partial import")
		}
	}
	metadata := s.db.Sources["wiki"].Metadata
	if kinds["wiki"] != "wiki" || kinds["program"] != "program" || metadata == nil || metadata.Title != "Handbook" {
		t.Fatal("source classification or metadata differs")
	}
	if got := request(Wiki{Store: s, Public: true}, "GET", "/media/wiki/img/icon.png", "", false); got.Code != 200 || got.Header().Get("Content-Type") != "image/png" {
		t.Fatal("wiki image unavailable", got.Code)
	}
	wikiPath, programPath := "", ""
	for _, page := range s.snapshot(false).Pages {
		if page.Environment == "wiki" && wikiPath == "" {
			wikiPath = strings.TrimPrefix(page.Path, "wiki/")
		}
		if page.Environment == "program" {
			p := strings.TrimPrefix(page.Path, "program/")
			if !strings.HasPrefix(p, "docs/") {
				t.Fatal("program included a path outside docs")
			}
			if programPath == "" {
				programPath = p
			}
		}
	}
	if wikiPath == "" || programPath == "" {
		t.Fatal("documentation absent from a source")
	}
	key, err := s.createAccessKey("fixture", []string{"wiki", "program"}, time.Now().Add(time.Hour))
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
	m = newSyncManager(s, github.NewForTest(proxy.URL, proxy.Client()))
	m.Once(ctx)
	if len(s.sources(true)) != 2 {
		t.Fatal("reopened sources failed fresh verification")
	}
	before, err := s.article("wiki", wikiPath, false)
	if err != nil {
		t.Fatal("local wiki article unavailable")
	}
	failWiki.Store(true)
	m.Once(ctx)
	if _, err = s.page("wiki/"+wikiPath, true); err != ErrNotFound {
		t.Fatal("outage left wiki publicly verified")
	}
	if _, err = s.page("program/"+programPath, true); err != nil {
		t.Fatal("outage affected independent program source")
	}
	during, err := s.article("wiki", wikiPath, false)
	if err != nil || len(before.Versions) != len(during.Versions) || before.Versions[len(before.Versions)-1].SHA != during.Versions[len(during.Versions)-1].SHA {
		t.Fatal("outage changed local wiki revision")
	}
	failWiki.Store(false)
	if err = s.retryConnection("wiki"); err != nil {
		t.Fatal("wiki retry failed")
	}
	m.Once(ctx)
	if _, err = s.page("wiki/"+wikiPath, true); err != nil {
		t.Fatal("wiki did not recover after fresh verification")
	}
	server := httptest.NewServer(Wiki{Store: s, Token: "owner"})
	defer server.Close()
	keyPath := filepath.Join(t.TempDir(), "reader.key")
	if err = os.WriteFile(keyPath, []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs("../../mcp/server.py")
	if err != nil {
		t.Fatal(err)
	}
	script := `import asyncio,json,sys
from mcp import ClientSession,StdioServerParameters
from mcp.client.stdio import stdio_client
async def main():
 p=StdioServerParameters(command=sys.executable,args=[sys.argv[1],'--url',sys.argv[2],'--reader-key-file',sys.argv[3]])
 async with stdio_client(p) as (r,w):
  async with ClientSession(r,w) as s:
   await s.initialize()
   async def call(name,args):
    r=await s.call_tool(name,args)
    if r.isError:raise RuntimeError('tool failed: '+name)
    return json.loads(r.content[0].text)
   assert {x['id'] for x in (await call('veda_sources',{}))['sources']}=={'wiki','program'}
   for ident,path in [('wiki',sys.argv[4]),('program',sys.argv[5])]:
    hits=await call('veda_find',{'source_id':ident,'query':'synthetic' if ident=='wiki' else 'docs','limit':1})
    assert hits['total']>0
    article=await call('veda_read',{'source_id':ident,'path':path})
    proposal=await call('veda_prepare_update',{'source_id':ident,'path':path,'expected_revision':article['revision'],'text':article['text']})
    assert proposal['saved'] is False and proposal['base_sha256']==article['sha256']
asyncio.run(main())`
	cmd := exec.CommandContext(ctx, python, "-c", script, adapter, server.URL, keyPath, wikiPath, programPath)
	if _, err := cmd.CombinedOutput(); err != nil {
		t.Fatal("MCP acceptance failed")
	}
	t.Logf("public mixed-source import, image, SQLite restart, simulated outage recovery and scoped MCP reads/proposals passed; %d pages", len(s.snapshot(false).Pages))
}
