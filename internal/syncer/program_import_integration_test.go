package syncer

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func TestProgramImportAndUpstreamChange(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"README.md": "# Not imported", "docs/setup.md": "# Setup\n\nRun `install`.", "src/main.go": "package example"})
	f.Modify(func() { f.Private = true })
	manager := connectFixture(t, s, f, true)
	page, err := s.page("handbook/docs/setup.md", false)
	if err != nil || page.Text != "# Setup\n\nRun `install`." {
		t.Fatal("documentation not imported", err)
	}
	if _, err = s.article("handbook", "README.md", false); err != domain.ErrNotFound {
		t.Fatal("non-docs content imported", err)
	}
	f.Advance(map[string]string{"README.md": "# Not imported", "docs/setup.md": "# Setup revised\n\nRun `install`.", "src/main.go": "package example"})
	syncFixtureSource(t, manager, "handbook")
	page, err = s.page("handbook/docs/setup.md", false)
	if err != nil || page.Text != "# Setup revised\n\nRun `install`." {
		t.Fatal("upstream change not shown", err)
	}
}
