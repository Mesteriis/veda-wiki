package agentctx

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestScopedMoveLocksMatchOwnerWithoutRevealingOtherSource(t *testing.T) {
	s := newScopeFixture(t)
	for _, id := range []string{"source", "destination"} {
		storetest.Mutate(t, s.st, func(db *domain.Database) {
			db.Sources[id] = domain.Source{ID: id, Title: id, Kind: "program", Visibility: "private"}
		})
		for _, p := range []string{"docs/item.md", "docs/dependency.md", "docs/unrelated.md"} {
			if _, err := s.put(id, p, "# Original", 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	keys := map[string]string{}
	for _, id := range []string{"source", "destination"} {
		key, err := s.auth().CreateAccessKey(id, []string{id}, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		keys[id] = key
	}
	storetest.Mutate(t, s.st, func(db *domain.Database) {
		db.Moves = map[string]domain.Move{"private-move-id": {State: "deleting_source", SourceID: "source", SourcePath: "docs/item.md", DestinationID: "destination", DestinationPath: "docs/item.md", Links: map[string]string{"docs/dependency.md": "private-link-hash"}, PreparedDeletion: "private-commit-id"}}
	})
	for _, tc := range []struct {
		source, path string
		blocked      bool
	}{
		{"source", "docs/item.md", true}, {"source", "docs/dependency.md", false},
		{"destination", "docs/item.md", true}, {"destination", "docs/dependency.md", true},
		{"destination", "docs/unrelated.md", false},
	} {
		view, valid, err := s.scopedView(keys[tc.source], time.Now())
		if err != nil || !valid {
			t.Fatal("scoped view unavailable", err)
		}
		result, err := view.proposalBase(tc.source, tc.path)
		if err != nil || result["write_blocked_by_move"] != tc.blocked {
			t.Fatal("incorrect scoped move status", tc.source, tc.path, err)
		}
		ownerResult, err := s.proposalBase(tc.source, tc.path)
		if err != nil || ownerResult["write_blocked_by_move"] != tc.blocked {
			t.Fatal("incorrect owner move status", tc.source, tc.path, err)
		}
		encoded, err := json.Marshal(result)
		if err != nil || strings.Contains(string(encoded), "private-move-id") || strings.Contains(string(encoded), "private-commit-id") || strings.Contains(string(encoded), "private-link-hash") {
			t.Fatal("move details exposed")
		}
		other := "source"
		if tc.source == "source" {
			other = "destination"
		}
		if strings.Contains(string(encoded), `"`+other+`"`) {
			t.Fatal("other source exposed")
		}
	}
}

// scopeFixture is a store with its import observations, as the HTTP layer
// pairs them, or an access key's scoped view of both.
type scopeFixture struct {
	st  *store.Store
	obs *importer.Observations
}

func newScopeFixture(t *testing.T) *scopeFixture {
	return &scopeFixture{st: storetest.New(t), obs: &importer.Observations{}}
}

func (s *scopeFixture) auth() auth.Service { return auth.Service{Store: s.st} }

// put commits a local edit as the owner API does.
func (s *scopeFixture) put(sourceID, p, text string, expected int) (domain.Revision, error) {
	var rev domain.Revision
	err := s.st.Update(func(tx *store.Tx) error {
		written, key, err := domain.PutArticle(tx.DB(), sourceID, p, text, expected, time.Now())
		if key != "" {
			tx.OnlyArticle(key)
		}
		rev = written
		return err
	})
	return rev, err
}

// scopedView is the access key's snapshot with observations of its sources only.
func (s *scopeFixture) scopedView(token string, now time.Time) (*scopeFixture, bool, error) {
	view, valid, err := s.auth().ScopedView(token, now)
	if view == nil {
		return nil, valid, err
	}
	var granted []string
	_ = view.View(func(db *domain.Database) error {
		for id := range db.Sources {
			granted = append(granted, id)
		}
		return nil
	})
	return &scopeFixture{st: view, obs: s.obs.Scoped(granted)}, valid, err
}

func (s *scopeFixture) proposalBase(sourceID, path string) (map[string]any, error) {
	return ProposalBase(s.st, s.obs, sourceID, path)
}
