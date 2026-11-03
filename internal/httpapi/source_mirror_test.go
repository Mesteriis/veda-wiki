package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func mirrorFixture(t *testing.T) (*Store, *syncer.Manager, *githubtest.Fake) {
	t.Helper()
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	return s, m, f
}

func TestMirrorConfigurationGuardsAndOwnerAPI(t *testing.T) {
	s, m, _ := mirrorFixture(t)
	enabled := true
	expected := 0
	in := syncer.MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &enabled, ExpectedRevision: &expected}
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
	if _, err := s.configureMirror(context.Background(), in); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale configuration accepted", err)
	}
	paused := false
	expected = 1
	if _, err := s.configureMirror(context.Background(), syncer.MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &paused, ExpectedRevision: &expected}); err != nil {
		t.Fatal(err)
	}
	// internal/syncer TestPausedMirrorRejectsStaleWorker covers a worker that
	// still holds the enabled revision.
	if r := request(w, "GET", "/api/v1/sources/mirrors", "", true); r.Code != 200 || !strings.Contains(r.Body.String(), "paused") {
		t.Fatal("owner status unavailable", r.Code)
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
	if s.mirrors()[0].State != "ready" || s.DB().Articles["handbook/a.md"].ReconcileBase == nil {
		t.Fatal("MCP-configured mirror did not run")
	}
}

func TestMirrorAdminSessionBoundaries(t *testing.T) {
	s, m, _ := mirrorFixture(t)
	w := Wiki{Store: s, Token: "owner", AdminSessions: &auth.Sessions{}, Sync: m}
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

func TestLocalImportWakesEnabledMirrorOnlyForChanges(t *testing.T) {
	s := testStore(t)
	b := testBatch()
	b.SourceID = "local"
	if _, err := s.importBatch(b); err != nil {
		t.Fatal(err)
	}
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Initial"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	yes := true
	revision := 0
	if _, err := s.configureMirror(context.Background(), syncer.MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &yes, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	m.Woken()
	current := s.DB().Sources["local"].ImportGeneration
	b.Create = false
	b.Complete = false
	b.ExpectedImport = &current
	b.Files["README.md"] = "# Changed"
	encoded, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(encoded))
	r = r.WithContext(context.WithValue(r.Context(), LocalRootKey{}, true))
	out := httptest.NewRecorder()
	Wiki{Store: s, ReaderLocal: true, LocalImportSource: "local", Sync: m}.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("local import failed", out.Code)
	}
	if !m.Woken() {
		t.Fatal("mirror not awakened")
	}
	current = s.DB().Sources["local"].ImportGeneration
	b.ExpectedImport = &current
	encoded, _ = json.Marshal(b)
	r = httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(encoded))
	r = r.WithContext(context.WithValue(r.Context(), LocalRootKey{}, true))
	out = httptest.NewRecorder()
	Wiki{Store: s, ReaderLocal: true, LocalImportSource: "local", Sync: m}.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("local replay failed", out.Code)
	}
	if m.Woken() {
		t.Fatal("unchanged import awakened mirror")
	}
	current = s.DB().Sources["local"].ImportGeneration
	b.ExpectedImport = &current
	b.Assets = map[string][]byte{"image.png": smallPNG(t)}
	encoded, _ = json.Marshal(b)
	r = httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(encoded))
	r = r.WithContext(context.WithValue(r.Context(), LocalRootKey{}, true))
	out = httptest.NewRecorder()
	Wiki{Store: s, ReaderLocal: true, LocalImportSource: "local", Sync: m}.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("asset-only import failed", out.Code)
	}
	if !m.Woken() {
		t.Fatal("asset-only import did not wake mirror")
	}
	no := false
	revision = 1
	if _, err = s.configureMirror(context.Background(), syncer.MirrorRequest{SourceID: "local", DestinationID: "handbook", Enabled: &no, ExpectedRevision: &revision}); err != nil {
		t.Fatal(err)
	}
	current = s.DB().Sources["local"].ImportGeneration
	b.ExpectedImport = &current
	b.Files["README.md"] = "# Changed while paused"
	encoded, _ = json.Marshal(b)
	r = httptest.NewRequest("POST", "/api/v1/import", bytes.NewReader(encoded))
	r = r.WithContext(context.WithValue(r.Context(), LocalRootKey{}, true))
	out = httptest.NewRecorder()
	Wiki{Store: s, ReaderLocal: true, LocalImportSource: "local", Sync: m}.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatal("paused local import failed", out.Code)
	}
	if m.Woken() {
		t.Fatal("paused mirror awakened")
	}
}
