package commands

import (
	"os"
	"testing"
)

func TestCreateSuperuserRejectsNonInteractiveTerminal(t *testing.T) {
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	defer func() { os.Stdin = old; r.Close(); w.Close() }()
	if err := CreateSuperuser([]string{"--username", "fixture"}); err == nil {
		t.Fatal("non-interactive use accepted")
	}
}
