//go:build !linux

package app

import "net"

// Local privileged import is deliberately unsupported outside Linux.
func peerIsRoot(net.Conn) bool { return false }
