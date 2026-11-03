package github

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestRepositoryNameAcceptsCommonLinksWithoutRelaxingBoundary(t *testing.T) {
	for _, input := range []string{"owner/repo", "owner/repo/", " https://github.com/owner/repo/ ", "https://github.com/owner/repo.git/", "https://github.com/owner/repo.git"} {
		if got, err := RepositoryName(input); err != nil || got != "owner/repo" {
			t.Fatalf("valid link %q: %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"https://github.com.evil.invalid/owner/repo/", "https://user@github.com/owner/repo/", "https://github.com/owner/repo/?token=bad", "https://github.com/owner/repo/#fragment", "http://github.com/owner/repo/", "https://github.com/owner/repo/tree/main/", "owner/repo//"} {
		if _, err := RepositoryName(input); err != domain.ErrInvalid {
			t.Fatalf("unsafe or ambiguous link accepted: %q", input)
		}
	}
}
