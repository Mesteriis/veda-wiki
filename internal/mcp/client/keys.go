package client

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// validKey reports whether s is exactly 64 lowercase hexadecimal characters.
func validKey(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ownerOnly reports whether info is owned by the current user and carries no
// group or other permission bits.
func ownerOnly(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid() && info.Mode().Perm()&0o077 == 0
}

// readKeyFile reads an access key. The file is opened without following a
// symlink and must be a regular owner-only file holding exactly 64 lowercase
// hexadecimal characters. At most 65 bytes are read.
func readKeyFile(path string) (string, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || !ownerOnly(info) {
		return "", errors.New("key must be an owner-only regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, 65))
	if err != nil {
		return "", err
	}
	if !validKey(string(data)) {
		return "", errors.New("invalid key")
	}
	return string(data), nil
}

// ReadKeyFile applies the access-key file checks used by the client:
// an owner-only regular file, not a symlink, holding exactly 64 lowercase
// hexadecimal characters.
func ReadKeyFile(path string) (string, error) { return readKeyFile(path) }
