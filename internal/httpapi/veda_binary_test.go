package httpapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// builtVeda is the veda binary built once for MCP integration tests.
var builtVeda struct {
	once sync.Once
	dir  string
	path string
	err  error
	out  []byte
}

func TestMain(m *testing.M) {
	code := m.Run()
	if builtVeda.dir != "" {
		_ = os.RemoveAll(builtVeda.dir)
	}
	os.Exit(code)
}

// vedaBinary returns the binary that serves `veda mcp`: VEDA_TEST_BINARY when
// set, otherwise ./cmd/veda built once into a temporary directory.
func vedaBinary(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("VEDA_TEST_BINARY"); path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
		return abs
	}
	builtVeda.once.Do(func() {
		builtVeda.dir, builtVeda.err = os.MkdirTemp("", "veda-mcp-test-")
		if builtVeda.err != nil {
			return
		}
		builtVeda.path = filepath.Join(builtVeda.dir, "veda")
		builtVeda.out, builtVeda.err = exec.Command("go", "build", "-o", builtVeda.path, "../../cmd/veda").CombinedOutput()
	})
	if builtVeda.err != nil {
		t.Fatalf("build veda: %v\n%s", builtVeda.err, builtVeda.out)
	}
	return builtVeda.path
}
