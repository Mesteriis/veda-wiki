package main

import (
	"context"
	"testing"
)

func TestRepositoryNameAcceptsCommonLinksWithoutRelaxingBoundary(t *testing.T) {
	for _, input := range []string{"owner/repo", "owner/repo/", " https://github.com/owner/repo/ ", "https://github.com/owner/repo.git/", "https://github.com/owner/repo.git"} {
		if got, err := repositoryName(input); err != nil || got != "owner/repo" {
			t.Fatalf("valid link %q: %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"https://github.com.evil.invalid/owner/repo/", "https://user@github.com/owner/repo/", "https://github.com/owner/repo/?token=bad", "https://github.com/owner/repo/#fragment", "http://github.com/owner/repo/", "https://github.com/owner/repo/tree/main/", "owner/repo//"} {
		if _, err := repositoryName(input); err != ErrInvalid {
			t.Fatalf("unsafe or ambiguous link accepted: %q", input)
		}
	}
}

func TestConnectAcceptsGitHubLinkWithTrailingSlash(t *testing.T) {
	s := testStore(t)
	f := newFakeGitHub(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Fixture"})
	m := newSyncManager(s, f.client())
	c, err := m.connect(context.Background(), connectRequest{ID: "handbook", Repository: " https://github.com/org/handbook/ "})
	if err != nil || c.Repository != "org/handbook" || c.Private || c.Write {
		t.Fatal("public link did not connect", err)
	}
	m.Once(context.Background())
	page, err := s.page("handbook/README.md", true)
	if err != nil || page.Text != "# Fixture" {
		t.Fatal("connected link did not import", err)
	}
}
