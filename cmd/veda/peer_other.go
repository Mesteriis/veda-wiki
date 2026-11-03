//go:build !linux

package main

import "net"

// Local privileged import is deliberately unsupported outside Linux.
func peerIsRoot(net.Conn) bool { return false }
