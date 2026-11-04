package commands

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRelayHelperProcess is the child the relay tests start: the test binary
// re-executed with VEDA_RELAY_HELPER selecting a behaviour.
func TestRelayHelperProcess(t *testing.T) {
	mode := os.Getenv("VEDA_RELAY_HELPER")
	if mode == "" {
		t.Skip("helper process only")
	}
	switch mode {
	case "echo":
		key := make([]byte, 64)
		if _, err := io.ReadFull(os.Stdin, key); err != nil {
			os.Exit(90)
		}
		os.Stdout.WriteString("key:" + string(key) + "\n")
		io.Copy(os.Stdout, os.Stdin)
	case "dump":
		os.Stdout.WriteString(strings.Join(os.Args, "\n") + "\n" + strings.Join(os.Environ(), "\n") + "\n")
	case "exit3":
		os.Stderr.WriteString("child complaint\n")
		os.Exit(3)
	}
	os.Exit(0)
}

func relayChild(t *testing.T, mode string) []string {
	t.Helper()
	t.Setenv("VEDA_RELAY_HELPER", mode)
	return []string{"--", os.Args[0], "-test.run=^TestRelayHelperProcess$"}
}

func relayKeyFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(mcpTestKey), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRelaySendsKeyFirstAndForwards(t *testing.T) {
	args := append([]string{"--key-file", relayKeyFile(t, 0o600)}, relayChild(t, "echo")...)
	var out, errOut bytes.Buffer
	if err := MCPRelay(args, strings.NewReader("hello\nworld\n"), &out, &errOut); err != nil {
		t.Fatalf("relay: %v (stderr %q)", err, errOut.String())
	}
	if want := "key:" + mcpTestKey + "\nhello\nworld\n"; out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
	if strings.Contains(errOut.String(), mcpTestKey) {
		t.Fatal("key leaked to stderr")
	}
}

func TestRelayKeepsKeyOutOfArgvAndEnv(t *testing.T) {
	args := append([]string{"--key-file", relayKeyFile(t, 0o600)}, relayChild(t, "dump")...)
	var out bytes.Buffer
	if err := MCPRelay(args, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "-test.run=") {
		t.Fatalf("child did not run: %q", out.String())
	}
	if strings.Contains(out.String(), mcpTestKey) {
		t.Fatal("key present in child argv or environment")
	}
}

func TestRelayPropagatesChildExitCodeAndStderr(t *testing.T) {
	args := append([]string{"--key-file", relayKeyFile(t, 0o600)}, relayChild(t, "exit3")...)
	var errOut bytes.Buffer
	err := MCPRelay(args, strings.NewReader("ignored"), io.Discard, &errOut)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 3 {
		t.Fatalf("err = %v, want exit code 3", err)
	}
	if !strings.Contains(errOut.String(), "child complaint") {
		t.Fatalf("child stderr not passed through: %q", errOut.String())
	}
}

func TestRelayRefusesBadKeyFileAndUsage(t *testing.T) {
	child := relayChild(t, "echo")
	for name, args := range map[string][]string{
		"group readable": append([]string{"--key-file", relayKeyFile(t, 0o640)}, child...),
		"missing file":   append([]string{"--key-file", filepath.Join(t.TempDir(), "none")}, child...),
		"no key flag":    child,
		"no command":     {"--key-file", relayKeyFile(t, 0o600)},
		"no command sep": {"--key-file", relayKeyFile(t, 0o600), "--"},
	} {
		var out, errOut bytes.Buffer
		err := MCPRelay(args, strings.NewReader(""), &out, &errOut)
		if err == nil {
			t.Fatalf("%s: relay succeeded", name)
		}
		if out.Len() != 0 || strings.Contains(err.Error()+errOut.String(), mcpTestKey) {
			t.Fatalf("%s: unexpected output %q / %v", name, out.String(), err)
		}
	}
}

func TestRelayMissingChildCommand(t *testing.T) {
	args := []string{"--key-file", relayKeyFile(t, 0o600), "--", filepath.Join(t.TempDir(), "absent")}
	if err := MCPRelay(args, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("expected start failure")
	}
}
