package syncer

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

func mirrorFixture(t *testing.T) (*fixtureStore, *Manager, *githubtest.Fake) {
	t.Helper()
	s := testStore(t)
	source(t, s, "local", "private")
	s.put("local", "a.md", "# Same", 0)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "a.md": "# Same"})
	f.Modify(func() { f.Private = true })
	m := connectFixture(t, s, f, true)
	return s, m, f
}
