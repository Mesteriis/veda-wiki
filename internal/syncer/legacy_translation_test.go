package syncer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

const legacyTranslationRecord = `{"origin":"local_model","language":"ru","original_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","text":"# Перевод","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","created_at":"2026-01-01T00:00:00Z","public_approved":true}`

// A pull over a connected program source whose stored article still holds the
// removed "translation" key succeeds and drops the key from the record it
// writes.
func TestGitHubPullOverLegacyTranslationKeyDropsIt(t *testing.T) {
	for _, sqlite := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sqlite"}[sqlite], func(t *testing.T) {
			s := testStore(t)
			f := githubtest.New(t, map[string]string{"docs/setup.md": "# Setup"})
			f.Modify(func() { f.Private = true })
			connectFixture(t, s, f, true)
			if sqlite {
				if err := store.MigrateSQLite(s.Dir()); err != nil {
					t.Fatal(err)
				}
				db := storetest.SQLite(t, s.Dir())
				_, err := db.Exec(`UPDATE records SET data=json_set(data,'$.translation',json(?)) WHERE category='article'`, legacyTranslationRecord)
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				injectJSONTranslation(t, s.Dir())
			}
			reopened, err := s.reopen()
			if err != nil || reopened.st.IsSQLite() != sqlite {
				t.Fatal("store with a legacy translation key did not open", err)
			}
			manager := newSyncManager(reopened, f.Client())
			f.Advance(map[string]string{"docs/setup.md": "# Setup revised"})
			syncFixtureSource(t, manager, "handbook")
			page, err := reopened.page("handbook/docs/setup.md", false)
			if err != nil || page.Text != "# Setup revised" {
				t.Fatal("pull over a legacy article failed", err)
			}
			again, err := reopened.reopen()
			if err != nil {
				t.Fatal(err)
			}
			if page, err = again.page("handbook/docs/setup.md", false); err != nil || page.Text != "# Setup revised" {
				t.Fatal("pulled article lost on reopen", err)
			}
			if sqlite {
				db := storetest.SQLite(t, s.Dir())
				defer db.Close()
				var data string
				if err = db.QueryRow(`SELECT data FROM records WHERE category='article' AND id='handbook/docs/setup.md'`).Scan(&data); err != nil || strings.Contains(data, `"translation`) {
					t.Fatal("pulled record kept the translation key", err, data)
				}
				return
			}
			raw, err := os.ReadFile(filepath.Join(s.Dir(), "state.json"))
			if err != nil || strings.Contains(string(raw), `"translation`) {
				t.Fatal("state kept the translation key after the pull", err)
			}
		})
	}
}

func injectJSONTranslation(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "state.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]json.RawMessage
	var articles map[string]map[string]json.RawMessage
	if err = json.Unmarshal(raw, &state); err != nil || json.Unmarshal(state["articles"], &articles) != nil || len(articles) == 0 {
		t.Fatal("fixture has no articles", err)
	}
	for _, a := range articles {
		a["translation"] = json.RawMessage(legacyTranslationRecord)
	}
	if state["articles"], err = json.Marshal(articles); err != nil {
		t.Fatal(err)
	}
	if raw, err = json.Marshal(state); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
