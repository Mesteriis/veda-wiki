package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReaderSocketRequiresPrivateOwnedDirectoryAndNoExistingPath(t *testing.T) {
	parent, err := os.MkdirTemp("/tmp", "veda-socket-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(parent) })
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	name := filepath.Join(parent, "reader.sock")
	l, e := listenReaderSocket(name)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Lstat(name)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("socket permissions", e)
	}
	if _, e := listenReaderSocket(name); e == nil {
		t.Fatal("existing path replaced")
	}
	l.Close()
	if e := os.Chmod(parent, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := listenReaderSocket(name); e == nil {
		t.Fatal("non-private parent accepted")
	}
}
