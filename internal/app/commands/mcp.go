package commands

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Mesteriis/veda-wiki/internal/mcp"
	"github.com/Mesteriis/veda-wiki/internal/mcp/client"
)

const mcpUsage = "usage: veda mcp [--url URL] (--key-file FILE | --reader-key-file FILE | " +
	"--reader-socket PATH [--reader-key-file FILE | --reader-key-preamble]) [--allow-remote-reader-https]"

// errMCPUsage reports invalid veda mcp arguments.
var errMCPUsage = errors.New(mcpUsage)

// MCP serves the VEDA MCP tools over stdin and stdout until the client
// disconnects. With --reader-key-preamble the first 64 bytes of stdin are
// the reader key; MCP messages follow them.
func MCP(args []string, stdin io.Reader, stdout io.Writer) error {
	c, err := mcpClient(args, stdin)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	transport := &sdk.IOTransport{Reader: io.NopCloser(stdin), Writer: nopWriteCloser{stdout}}
	if err := mcp.NewServer(c).Run(ctx, transport); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// mcpClient applies the former adapter's argument rules: at most one of
// --key-file, --reader-key-file and --reader-key-preamble; a key file or a
// reader socket is required; an owner key file cannot use the reader socket;
// the preamble needs the reader socket.
func mcpClient(args []string, stdin io.Reader) (*client.Client, error) {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	url := flags.String("url", "http://127.0.0.1:8080", "Loopback VEDA endpoint")
	keyFile := flags.String("key-file", "", "Owner key file")
	readerKeyFile := flags.String("reader-key-file", "", "Reader key file")
	preamble := flags.Bool("reader-key-preamble", false, "Read the reader key from the first 64 bytes of stdin")
	socket := flags.String("reader-socket", "", "Private reader Unix socket")
	allowHTTPS := flags.Bool("allow-remote-reader-https", false, "Allow a reader key over TLS-verified HTTPS")
	editor := flags.Bool("editor", false, "With the owner key: expose only reading and article writing tools")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return nil, errMCPUsage
	}
	exclusive := 0
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "key-file" || f.Name == "reader-key-file" || f.Name == "reader-key-preamble" {
			exclusive++
		}
	})
	if exclusive > 1 || (*keyFile == "" && *readerKeyFile == "" && *socket == "") ||
		(*keyFile != "" && *socket != "") || (*preamble && *socket == "") || (*editor && *keyFile == "") {
		return nil, errMCPUsage
	}
	opts := client.Options{URL: *url, KeyFile: *keyFile, ReaderKeyFile: *readerKeyFile, SocketPath: *socket,
		AllowRemoteReaderHTTPS: *allowHTTPS, Editor: *editor}
	if *preamble {
		key, err := readKeyPreamble(stdin)
		if err != nil {
			return nil, err
		}
		opts.KeyValue = key
	}
	return client.New(opts)
}

// readKeyPreamble reads exactly 64 bytes, never more, so the MCP messages
// that follow stay unread, and requires lowercase hexadecimal.
func readKeyPreamble(stdin io.Reader) (string, error) {
	raw := make([]byte, 64)
	if _, err := io.ReadFull(stdin, raw); err != nil {
		return "", errors.New("missing reader key preamble")
	}
	for _, b := range raw {
		if !('0' <= b && b <= '9' || 'a' <= b && b <= 'f') {
			return "", errors.New("invalid reader key preamble")
		}
	}
	return string(raw), nil
}
