package client

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
)

// checkSocket requires the reader socket and its directory to be private and
// owned by the current user. It runs before every request.
func checkSocket(path string) error {
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !parent.IsDir() || !ownerOnly(parent) || info.Mode().Type() != os.ModeSocket || !ownerOnly(info) {
		return errors.New("reader socket must be private and owned by this user")
	}
	return nil
}

// dialSocket returns a dialer that always connects to the given Unix socket.
func dialSocket(path string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", path)
	}
}
