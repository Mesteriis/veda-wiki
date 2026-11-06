package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

var knowledgeReadTools = []string{"veda_entities", "veda_entity", "veda_article_links", "veda_graph", "veda_graph_context"}

var knowledgeOwnerTools = []string{"veda_knowledge_status", "veda_knowledge_reindex"}

// Every knowledge tool calls its REST route with the translated arguments
// and returns the answer unchanged.
func TestKnowledgeToolsCallREST(t *testing.T) {
	routes := map[string]string{
		"GET /api/v1/knowledge/entities": `{"entities":[{"id":"ent-1","type":"host","name":"db1"}],"total":1}`,
		"GET /api/v1/knowledge/entity":   `{"id":"ent-1","claims":[],"truncated":false}`,
		"GET /api/v1/knowledge/article":  `{"key":{"source_id":"a","path":"b.md"},"status":"processing"}`,
		"GET /api/v1/knowledge/graph":    `{"nodes":[],"edges":[],"truncated":false}`,
		"GET /api/v1/knowledge/context":  `{"items":[],"bytes":2,"truncated":false,"query":"db1","depth":1,"limit_bytes":900}`,
		"GET /api/v1/knowledge/status":   `{"enabled":true,"state":"ready"}`,
		"POST /api/v1/knowledge/reindex": `{"operation_id":"reindex-1","requeued":1}`,
	}
	for _, tc := range []struct {
		tool, args, method, uri, body string
		readOnly                      bool
	}{
		{"veda_entities", `{"query":"db","source_id":"a","type":"host","limit":2,"offset":4}`, "GET", "/api/v1/knowledge/entities?limit=2&offset=4&q=db&source_id=a&type=host", "", true},
		{"veda_entities", `{"query":"Ö b"}`, "GET", "/api/v1/knowledge/entities?q=%C3%96+b", "", true},
		{"veda_entity", `{"id":"ent-1","limit":3}`, "GET", "/api/v1/knowledge/entity?id=ent-1&limit=3", "", true},
		{"veda_article_links", `{"source_id":"a","path":"b.md"}`, "GET", "/api/v1/knowledge/article?path=b.md&source_id=a", "", true},
		{"veda_graph", `{"entity_id":"ent-1","depth":2,"max_nodes":200,"max_edges":400}`, "GET", "/api/v1/knowledge/graph?depth=2&entity_id=ent-1&max_edges=400&max_nodes=200", "", true},
		{"veda_graph", `{"source_id":"a","path":"b.md"}`, "GET", "/api/v1/knowledge/graph?path=b.md&source_id=a", "", true},
		{"veda_graph_context", `{"query":"db1","depth":1,"limit_bytes":900}`, "GET", "/api/v1/knowledge/context?depth=1&limit_bytes=900&q=db1", "", true},
		{"veda_knowledge_status", `{}`, "GET", "/api/v1/knowledge/status", "", false},
		{"veda_knowledge_reindex", `{"scope":"all"}`, "POST", "/api/v1/knowledge/reindex", `{"scope":"all"}`, false},
		{"veda_knowledge_reindex", `{"scope":"article","source_id":"a","path":"b.md","force":true}`, "POST", "/api/v1/knowledge/reindex", `{"force":true,"path":"b.md","scope":"article","source_id":"a"}`, false},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			fake, base := startREST(t, routes)
			got := success(t, call(t, connect(t, base, tc.readOnly), tc.tool, tc.args))
			requests := fake.all()
			if len(requests) != 1 || requests[0].Method != tc.method || requests[0].URI != tc.uri || requests[0].Body != tc.body {
				t.Fatalf("requests %+v", requests)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(routes[tc.method+" "+strings.Split(tc.uri, "?")[0]]), &want); err != nil {
				t.Fatal(err)
			}
			if !jsonEqual(got, want) {
				t.Fatalf("result %v", got)
			}
		})
	}
}

// A reader key lists only the knowledge reads, and a direct call to an
// owner tool is refused before any request (§12 case 11).
func TestKnowledgeReaderCannotUseOwnerTools(t *testing.T) {
	fake, base := startREST(t, map[string]string{"GET /api/v1/knowledge/status": `{}`, "POST /api/v1/knowledge/reindex": `{}`})
	session := connect(t, base, true)
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	for _, name := range knowledgeReadTools {
		if !names[name] {
			t.Errorf("reader does not list %s", name)
		}
	}
	for _, name := range knowledgeOwnerTools {
		if names[name] {
			t.Errorf("reader lists %s", name)
		}
	}
	for name, args := range map[string]string{"veda_knowledge_status": `{}`, "veda_knowledge_reindex": `{"scope":"all","force":true}`} {
		if got := refusal(t, call(t, session, name, args)); got != genericRefusal {
			t.Errorf("%s: %q", name, got)
		}
	}
	if fake.count() != 0 {
		t.Fatalf("refused calls sent %d requests", fake.count())
	}
}

// Unknown arguments, values out of the REST bounds and inconsistent targets
// are refused before any request, naming properties but never values.
func TestKnowledgeArgumentsRejected(t *testing.T) {
	long := strings.Repeat("ж", 201)
	for _, tc := range []struct{ tool, args, want string }{
		{"veda_entities", `{"query":"db","cypher":"MATCH (n) RETURN n"}`, "unknown property 'cypher'"},
		{"veda_entities", `{}`, "missing required property 'query'"},
		{"veda_entities", `{"query":""}`, "property 'query' is too short"},
		{"veda_entities", `{"query":"` + long + `"}`, "property 'query' is too long"},
		{"veda_entities", `{"query":"db","type":"secret-type"}`, "property 'type' is not an allowed value"},
		{"veda_entities", `{"query":"db","limit":0}`, "property 'limit' out of range"},
		{"veda_entities", `{"query":"db","limit":101}`, "property 'limit' out of range"},
		{"veda_entities", `{"query":"db","offset":10001}`, "property 'offset' out of range"},
		{"veda_entity", `{"id":"ent-1","limit":101}`, "property 'limit' out of range"},
		{"veda_entity", `{"id":"ent-1","sha256":"x"}`, "unknown property 'sha256'"},
		{"veda_article_links", `{"source_id":"a"}`, "missing required property 'path'"},
		{"veda_graph", `{}`, "provide either 'entity_id' or both 'source_id' and 'path'"},
		{"veda_graph", `{"entity_id":"e","source_id":"a","path":"b"}`, "provide either 'entity_id' or both 'source_id' and 'path'"},
		{"veda_graph", `{"source_id":"a"}`, "provide either 'entity_id' or both 'source_id' and 'path'"},
		{"veda_graph", `{"entity_id":"e","depth":3}`, "property 'depth' out of range"},
		{"veda_graph", `{"entity_id":"e","max_nodes":201}`, "property 'max_nodes' out of range"},
		{"veda_graph", `{"entity_id":"e","max_edges":401}`, "property 'max_edges' out of range"},
		{"veda_graph_context", `{"query":"db","limit_bytes":1}`, "property 'limit_bytes' out of range"},
		{"veda_graph_context", `{"query":"db","limit_bytes":16001}`, "property 'limit_bytes' out of range"},
		{"veda_graph_context", `{"query":"db","depth":0}`, "property 'depth' out of range"},
		{"veda_knowledge_status", `{"verbose":true}`, "unknown property 'verbose'"},
		{"veda_knowledge_reindex", `{"scope":"everything"}`, "property 'scope' is not an allowed value"},
		{"veda_knowledge_reindex", `{"scope":"all","source_id":"a"}`, "scope 'all' takes neither 'source_id' nor 'path'"},
		{"veda_knowledge_reindex", `{"scope":"source"}`, "scope 'source' takes 'source_id' and no 'path'"},
		{"veda_knowledge_reindex", `{"scope":"source","source_id":"a","path":"b"}`, "scope 'source' takes 'source_id' and no 'path'"},
		{"veda_knowledge_reindex", `{"scope":"article","source_id":"a"}`, "scope 'article' takes 'source_id' and 'path'"},
		{"veda_knowledge_reindex", `{"scope":"all","force":"yes"}`, "property 'force' has the wrong type"},
	} {
		fake, base := startREST(t, nil)
		got := refusal(t, call(t, connect(t, base, false), tc.tool, tc.args))
		if got != "Input validation error: "+tc.want || fake.count() != 0 {
			t.Errorf("%s %s: %q requests=%d", tc.tool, tc.args, got, fake.count())
		}
		if strings.Contains(got, "MATCH") || strings.Contains(got, "secret-type") || strings.Contains(got, "ж") {
			t.Errorf("%s: refusal echoes a value: %q", tc.tool, got)
		}
	}
}

// REST refusals become fixed messages: a disabled module says so, and no
// response text is echoed.
func TestKnowledgeRefusals(t *testing.T) {
	for _, tc := range []struct {
		status    int
		body, msg string
	}{
		{503, `{"error":"knowledge_disabled"}`, "VEDA request refused: the knowledge graph module is disabled on this VEDA server (HTTP 503, knowledge_disabled)."},
		{404, `{"error":"not_found"}`, "VEDA request refused: not found or not visible to this client (HTTP 404, not_found)."},
		{400, `{"error":"invalid_request"}`, "VEDA request refused: invalid input (HTTP 400, invalid_request)."},
		{500, `{"error":"neo4j said /private/canary"}`, "VEDA request refused: request failed (HTTP 500)."},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		session := connect(t, server.URL, true)
		for _, name := range knowledgeReadTools {
			args := `{"query":"q"}`
			switch name {
			case "veda_entity":
				args = `{"id":"ent-1"}`
			case "veda_article_links":
				args = `{"source_id":"a","path":"b.md"}`
			case "veda_graph":
				args = `{"entity_id":"ent-1"}`
			}
			if got := refusal(t, call(t, session, name, args)); got != tc.msg {
				t.Errorf("%s %d: %q", name, tc.status, got)
			}
		}
		server.Close()
	}
}

// An answer above MaxKnowledgeResultBytes is refused, not relayed.
func TestKnowledgeResultIsBounded(t *testing.T) {
	huge := `{"items":["` + strings.Repeat("x", MaxKnowledgeResultBytes) + `"]}`
	_, base := startREST(t, map[string]string{"GET /api/v1/knowledge/context": huge})
	got := refusal(t, call(t, connect(t, base, true), "veda_graph_context", `{"query":"q"}`))
	if got != errKnowledgeTooLarge.message {
		t.Fatalf("%q", got)
	}
}

// Every tool schema is byte-identical to its golden inputSchema (key order
// included), so the knowledge tools changed no existing contract.
func TestToolSchemasAreByteIdentical(t *testing.T) {
	raw, err := readGoldenRaw()
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string][]byte{}
	for _, tool := range raw.Owner {
		var compact bytes.Buffer
		if err := json.Compact(&compact, tool.InputSchema); err != nil {
			t.Fatal(err)
		}
		golden[tool.Name] = compact.Bytes()
	}
	if len(golden) != len(Tools) || len(Tools) != 33 {
		t.Fatalf("golden %d tools, table %d", len(golden), len(Tools))
	}
	for _, spec := range Tools {
		if !bytes.Equal(golden[spec.Name], spec.Schema) {
			t.Errorf("%s schema differs:\n%s\n%s", spec.Name, spec.Schema, golden[spec.Name])
		}
	}
}

type rawGolden struct {
	Owner []struct {
		Name        string          `json:"name"`
		InputSchema json.RawMessage `json:"inputSchema"`
	} `json:"owner"`
}

func readGoldenRaw() (rawGolden, error) {
	var g rawGolden
	data, err := os.ReadFile("testdata/tools.json")
	if err == nil {
		err = json.Unmarshal(data, &g)
	}
	return g, err
}
