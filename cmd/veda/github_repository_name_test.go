package main

import (
	"context"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"testing"
)

func TestConnectAcceptsGitHubLinkWithTrailingSlash(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Fixture"})
	m := newSyncManager(s, f.Client())
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
