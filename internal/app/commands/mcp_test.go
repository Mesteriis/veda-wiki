package commands

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpTestKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestMCPArgumentRules(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	socket := filepath.Join(t.TempDir(), "reader.sock")
	for _, tc := range []struct {
		args     []string
		ok, read bool
	}{
		{[]string{"--key-file", key}, true, false},
		{[]string{"--reader-key-file", key}, true, true},
		{[]string{"--reader-socket", socket}, true, true},
		{[]string{"--reader-socket", socket, "--reader-key-file", key}, true, true},
		{[]string{"--url", "https://veda.example", "--reader-key-file", key, "--allow-remote-reader-https"}, true, true},
		{nil, false, false},
		{[]string{"--key-file", key, "--reader-key-file", key}, false, false},
		{[]string{"--key-file", key, "--reader-key-preamble", "--reader-socket", socket}, false, false},
		{[]string{"--reader-key-file", key, "--reader-key-preamble", "--reader-socket", socket}, false, false},
		{[]string{"--key-file", key, "--reader-socket", socket}, false, false},
		{[]string{"--reader-key-preamble"}, false, false},
		{[]string{"--key-file", key, "extra"}, false, false},
		{[]string{"--key-file", key, "--unknown"}, false, false},
		{[]string{"--url", "https://veda.example", "--key-file", key, "--allow-remote-reader-https"}, false, false},
		{[]string{"--url", "http://example.com", "--key-file", key}, false, false},
		{[]string{"--reader-socket", "relative.sock"}, false, false},
		{[]string{"--editor", "--key-file", key}, true, false},
		{[]string{"--editor", "--reader-key-file", key}, false, false},
		{[]string{"--editor", "--reader-socket", socket}, false, false},
		{[]string{"--editor"}, false, false},
	} {
		c, err := mcpClient(tc.args, strings.NewReader(""))
		if (err == nil) != tc.ok || (err == nil && c.ReadOnly() != tc.read) {
			t.Fatalf("%v: err=%v", tc.args, err)
		}
		if err == nil && c.Editor() != (tc.args[0] == "--editor") {
			t.Fatalf("%v: editor=%v", tc.args, c.Editor())
		}
	}
}

func TestMCPEditorServesReadingAndArticleWritesOnly(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte(mcpTestKey), 0o600); err != nil {
		t.Fatal(err)
	}
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- MCP([]string{"--editor", "--key-file", key}, serverIn, serverOut)
		_ = serverOut.Close()
	}()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).
		Connect(t.Context(), &sdk.IOTransport{Reader: clientIn, Writer: clientOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	if len(tools.Tools) != 18 || !names["veda_write_article"] || !names["veda_edit_article"] || !names["veda_prepare_update"] || !names["veda_read"] {
		t.Fatalf("editor tools: %v", names)
	}
	for _, admin := range []string{"veda_github_connect", "veda_create_source", "veda_move_article", "veda_knowledge_reindex", "veda_resolve_conflict"} {
		if names[admin] {
			t.Fatalf("editor exposes %s", admin)
		}
	}
	result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "veda_github_status", Arguments: map[string]any{}})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("an administrative tool must be refused for an editor: %v %+v", err, result)
	}
	_ = session.Close()
	if err := <-done; err != nil {
		t.Fatalf("MCP returned %v after the client disconnected", err)
	}
}

func TestMCPKeyPreambleReadsExactly64Bytes(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "reader.sock")
	args := []string{"--reader-socket", socket, "--reader-key-preamble"}
	stdin := bytes.NewReader([]byte(mcpTestKey + `{"jsonrpc":"2.0"}`))
	c, err := mcpClient(args, stdin)
	if err != nil || !c.ReadOnly() {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(stdin); string(rest) != `{"jsonrpc":"2.0"}` {
		t.Fatalf("preamble consumed MCP input: %q", rest)
	}
	for _, input := range []string{"", mcpTestKey[:63], strings.ToUpper(mcpTestKey), strings.Repeat("g", 64), strings.Repeat("\x00", 64)} {
		if _, err := mcpClient(args, strings.NewReader(input)); err == nil {
			t.Fatalf("accepted preamble %q", input)
		}
	}
}

func TestMCPServesReaderToolsOverStreams(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte(mcpTestKey), 0o600); err != nil {
		t.Fatal(err)
	}
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- MCP([]string{"--reader-key-file", key}, serverIn, serverOut)
		_ = serverOut.Close()
	}()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).
		Connect(t.Context(), &sdk.IOTransport{Reader: clientIn, Writer: clientOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != 16 {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	_ = session.Close()
	if err := <-done; err != nil {
		t.Fatalf("MCP returned %v after the client disconnected", err)
	}
}
