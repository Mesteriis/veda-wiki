package commands

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

const mcpRelayUsage = "usage: veda mcp-relay --key-file FILE -- COMMAND [ARGS...]"

// relayGrace is how long the child gets to exit after its stdin closes, and
// again after SIGTERM, before it is killed.
const relayGrace = 5 * time.Second

// ExitError carries the exit status of a relayed child process.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("child exited with status %d", e.Code) }

// MCPRelay sends an access key over a trusted child's stdin, exactly 64 bytes
// and nothing else, then relays stdin and stdout both ways until the input
// ends. The child command is trusted administrator input (typically ssh). The
// key never reaches argv or the environment, and neither it nor the MCP
// traffic is logged. A non-zero child status is returned as *ExitError.
func MCPRelay(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("mcp-relay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	keyFile := flags.String("key-file", "", "Access key file")
	if err := flags.Parse(args); err != nil || *keyFile == "" || flags.NArg() == 0 {
		return errors.New(mcpRelayUsage)
	}
	key, err := client.ReadKeyFile(*keyFile)
	if err != nil {
		return fmt.Errorf("key file: %w", err)
	}
	command := flags.Args()
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		defer pipe.Close()
		if _, err := io.WriteString(pipe, key); err != nil {
			return
		}
		io.Copy(pipe, stdin) // a broken pipe means the child is gone
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-inputDone:
		waitErr = waitOrStop(cmd, waited)
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) {
		code := exit.ExitCode()
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			code = 128 + int(status.Signal())
		}
		return &ExitError{Code: code}
	}
	return waitErr
}

// waitOrStop waits for the child after its stdin closed: it is terminated
// after relayGrace and killed after another relayGrace.
func waitOrStop(cmd *exec.Cmd, waited <-chan error) error {
	for _, signal := range []syscall.Signal{0, syscall.SIGTERM, syscall.SIGKILL} {
		if signal != 0 {
			cmd.Process.Signal(signal)
		}
		select {
		case err := <-waited:
			return err
		case <-time.After(relayGrace):
		}
	}
	return <-waited
}
