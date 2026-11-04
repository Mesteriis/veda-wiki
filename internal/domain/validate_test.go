package domain

import (
	"strings"
	"testing"
)

// Path cases mirror TestSourcesStaySeparateAndPathsBounded in cmd/veda, which
// still checks them through the store write path.
func TestArticlePathBounds(t *testing.T) {
	for _, p := range []string{"../secret.md", "/etc/file.md", "a/../b.md", ".hidden/x.md", "x\\a.md", "x.txt", "", "a.md\n", strings.Repeat("a", 510) + ".md"} {
		if ArticlePath(p, "wiki") {
			t.Fatalf("accepted %q", p)
		}
	}
	for _, p := range []string{"same.md", "folder/doc.MD", "notes.markdown"} {
		if !ArticlePath(p, "wiki") {
			t.Fatalf("rejected %q", p)
		}
	}
	if ArticlePath("README.md", "program") || !ArticlePath("docs/guide.md", "program") {
		t.Fatal("program sources must keep articles under docs/")
	}
}

func TestSafeRelative(t *testing.T) {
	for _, p := range []string{"", "/abs.png", "a/../b.png", ".git/config", "a/.hidden", "a\\b", "a\x00b", "./a.png"} {
		if SafeRelative(p) {
			t.Fatalf("accepted %q", p)
		}
	}
	if !SafeRelative("images/logo.png") {
		t.Fatal("rejected a plain relative path")
	}
}

func TestIdentifierCommitAndCredentialChecks(t *testing.T) {
	if !ValidID("handbook-1") || ValidID("Handbook") || ValidID("-x") || ValidID(strings.Repeat("a", 65)) {
		t.Fatal("identifier check changed")
	}
	if !ValidCommitID(strings.Repeat("a", 40)) || !ValidCommitID(strings.Repeat("b", 64)) || ValidCommitID(strings.Repeat("a", 41)) || ValidCommitID(strings.Repeat("A", 40)) {
		t.Fatal("commit check changed")
	}
	if !CredentialLike("password=synthetic-fixture-only") || CredentialLike("# Plain text") {
		t.Fatal("credential check changed")
	}
	if !CredentialLikeBytes([]byte("password=synthetic-fixture-only")) || CredentialLikeBytes([]byte("# Plain text")) {
		t.Fatal("credential bytes check differs from the string check")
	}
	if _, err := HexKey(strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := HexKey(strings.Repeat("F", 64)); err != ErrInvalid {
		t.Fatal("uppercase key accepted")
	}
	if Digest([]byte("")) != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatal("digest changed")
	}
}
