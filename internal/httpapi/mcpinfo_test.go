package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
)

func labelled(w Wiki, method, url, body, token, label string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if label != "" {
		r.Header.Set(ClientHeader, label)
	}
	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, r)
	return rw
}

type activityAnswer struct {
	Clients []mcpClientSeen `json:"clients"`
	Writes  []mcpWrite      `json:"writes"`
}

func activityOf(t *testing.T, w Wiki) activityAnswer {
	t.Helper()
	got := labelled(w, "GET", "/api/v1/mcp/activity", "", "test-owner", "")
	var out activityAnswer
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &out) != nil {
		t.Fatalf("activity: %d %s", got.Code, got.Body.String())
	}
	return out
}

func TestMCPWritesAreRecordedWithTheirMode(t *testing.T) {
	s := testStore(t)
	source(t, s, "wiki", "private")
	w := Wiki{Store: s, Token: "test-owner", Activity: &ClientActivity{}}
	write := func(path, label string) {
		t.Helper()
		body := `{"source_id":"wiki","path":"` + path + `","text":"# Fixture","expected_revision":0}`
		if got := labelled(w, "POST", "/api/v1/articles", body, "test-owner", label); got.Code != 201 {
			t.Fatalf("write %s: %d %s", path, got.Code, got.Body.String())
		}
	}
	write("editor.md", "mcp/editor")
	write("owner.md", "mcp/owner")
	write("plain.md", "")
	db := s.state(t)
	for path, want := range map[string]string{"editor.md": "mcp-editor", "owner.md": "mcp-owner", "plain.md": "rest"} {
		if got := db.Articles["wiki/"+path].Versions[0].Via; got != want {
			t.Fatalf("%s via = %q, want %q", path, got, want)
		}
	}
	out := activityOf(t, w)
	if len(out.Writes) != 2 || out.Writes[0].Via == "rest" || out.Writes[1].Via == "rest" {
		t.Fatalf("MCP writes: %+v", out.Writes)
	}
	modes := map[string]bool{}
	for _, c := range out.Clients {
		modes[c.Mode] = true
		if _, err := time.Parse(time.RFC3339Nano, c.LastSeen); err != nil {
			t.Fatalf("last seen %q", c.LastSeen)
		}
	}
	if !modes["editor"] || !modes["owner"] || len(modes) != 2 {
		t.Fatalf("clients: %+v", out.Clients)
	}
}

func TestMCPLabelWithoutMatchingCredentialIsIgnored(t *testing.T) {
	s := testStore(t)
	source(t, s, "wiki", "private")
	w := Wiki{Store: s, Token: "test-owner", ReaderToken: "reader", Activity: &ClientActivity{}}
	labelled(w, "GET", "/api/v1/sources", "", "reader", "mcp/editor")
	labelled(w, "GET", "/api/v1/sources", "", "", "mcp/owner")
	labelled(w, "GET", "/api/v1/sources", "", "test-owner", "mcp/reader")
	if out := activityOf(t, w); len(out.Clients) != 0 {
		t.Fatalf("a label without its credential was recorded: %+v", out.Clients)
	}
	labelled(w, "GET", "/api/v1/sources", "", "reader", "mcp/reader")
	if out := activityOf(t, w); len(out.Clients) != 1 || out.Clients[0].Mode != "reader" {
		t.Fatalf("reader not recorded: %+v", out.Clients)
	}
}

func TestMCPRoutesAreForTheOwnerAndAdministrator(t *testing.T) {
	s := testStore(t)
	w := Wiki{Store: s, Token: "test-owner", ReaderToken: "reader", AdminSessions: &auth.Sessions{}}
	for _, token := range []string{"", "reader"} {
		if got := labelled(w, "GET", "/api/v1/mcp/tools", "", token, ""); got.Code != 404 {
			t.Fatalf("tools served to %q: %d", token, got.Code)
		}
	}
	if got := labelled(w, "GET", "/api/admin/mcp/tools", "", "", ""); got.Code != 404 {
		t.Fatalf("admin route without a session: %d", got.Code)
	}
	got := labelled(w, "GET", "/api/v1/mcp/tools", "", "test-owner", "")
	var out struct {
		Tools []mcpTool `json:"tools"`
	}
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &out) != nil {
		t.Fatalf("tools: %d", got.Code)
	}
	modes := map[string][]string{}
	for _, tool := range out.Tools {
		modes[tool.Name] = tool.Modes
	}
	if strings.Join(modes["veda_read"], ",") != "reader,editor,owner" ||
		strings.Join(modes["veda_write_article"], ",") != "editor,owner" ||
		strings.Join(modes["veda_github_connect"], ",") != "owner" {
		t.Fatalf("modes: %v", modes)
	}
	if got := labelled(w, "POST", "/api/v1/mcp/tools", "", "test-owner", ""); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("tools accepted a write: %d", got.Code)
	}
}

func TestStateShowsLastMCPContactToTheOwnerOnly(t *testing.T) {
	s := testStore(t)
	source(t, s, "wiki", "private")
	activity := &ClientActivity{}
	private := Wiki{Store: s, Token: "test-owner", ReaderLocal: true, Activity: activity}
	public := Wiki{Store: s, Public: true, Activity: activity}
	stateOf := func(w Wiki, token string) map[string]any {
		t.Helper()
		got := labelled(w, "GET", "/api/state", "", token, "")
		var out struct {
			View map[string]any `json:"view"`
		}
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &out) != nil {
			t.Fatalf("state: %d", got.Code)
		}
		return out.View
	}
	if _, present := stateOf(private, "test-owner")["MCPLastSeen"]; present {
		t.Fatal("last MCP contact reported before any contact")
	}
	labelled(private, "GET", "/api/v1/sources", "", "test-owner", "mcp/editor")
	seen, _ := stateOf(private, "test-owner")["MCPLastSeen"].(string)
	if at, err := time.Parse(time.RFC3339, seen); err != nil || at.Second() != 0 || time.Since(at) > 2*time.Minute {
		t.Fatalf("last MCP contact %q", seen)
	}
	if _, present := stateOf(private, "")["MCPLastSeen"]; present {
		t.Fatal("a reader without the owner key sees MCP activity")
	}
	if _, present := stateOf(public, "test-owner")["MCPLastSeen"]; present {
		t.Fatal("public state reveals MCP activity")
	}
}

func TestAdministratorSessionReadsTheMCPRoutes(t *testing.T) {
	s := testStore(t)
	groups := []string{}
	password := "synthetic password"
	user, err := s.auth().SaveUser("admin", &groups, &password, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.auth().SetUserAdmin("admin", true, user.Revision); err != nil {
		t.Fatal(err)
	}
	w := Wiki{Store: s, Token: "owner", AdminSessions: &auth.Sessions{}, AccessSessions: &auth.Sessions{}, Activity: &ClientActivity{}}
	call := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		if method == "POST" {
			r.Header.Set("Origin", "http://localhost:8080")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	login := call("POST", "/api/admin/session/login", `{"username":"admin","password":"synthetic password"}`, nil)
	if login.Code != 200 || len(login.Result().Cookies()) != 1 {
		t.Fatal("admin login failed", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	for _, path := range []string{"/api/admin/mcp/tools", "/api/admin/mcp/activity"} {
		if got := call("GET", path, "", cookie); got.Code != 200 {
			t.Fatalf("%s: %d", path, got.Code)
		}
	}
	if got := call("POST", "/api/admin/mcp/tools", "", cookie); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("admin route accepted a write: %d", got.Code)
	}
}
