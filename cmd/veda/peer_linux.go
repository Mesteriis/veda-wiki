//go:build linux

package main

import (
	"net"
	"syscall"
)

// Peer credentials are obtained from the kernel, not HTTP headers.
func peerIsRoot(conn net.Conn) bool {
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return false
	}
	root := false
	err = raw.Control(func(fd uintptr) {
		cred, e := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		root = e == nil && cred.Uid == 0
	})
	return err == nil && root
}
