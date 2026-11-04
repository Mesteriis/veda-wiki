package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

// legacyTranslation is a translation record as builds before its removal
// stored it on an article; legacyFingerprint is a move's translation digest.
const (
	legacyTranslation = `{"origin":"local_model","language":"ru","original_sha256":"` + legacyFingerprint + `","text":"# Перевод","sha256":"` + legacyFingerprint + `","created_at":"2026-01-01T00:00:00Z","public_approved":true}`
	legacyFingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// A store written before translation was removed keeps the format version
// but may still hold "translation" on articles and "translation_fingerprint"
// on moves. Both backends load such records, serve the original, import,
// copy and move them, and drop the keys from every record they write.
func TestLegacyTranslationKeysAreIgnoredAndDroppedOnWrite(t *testing.T) {
	for _, sqlite := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sqlite"}[sqlite], func(t *testing.T) {
			testLegacyTranslationKeys(t, sqlite)
		})
	}
}

func testLegacyTranslationKeys(t *testing.T, sqlite bool) {
	s := testStore(t)
	if _, err := s.importBatch(testBatch()); err != nil {
		t.Fatal(err)
	}
	programSource(t, s, "from", "private")
	programSource(t, s, "to", "private")
	for _, p := range []string{"docs/edit.md", "docs/copy.md", "docs/move.md", "docs/moved-before.md"} {
		if _, err := s.put("from", p, "# Original "+p, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "docs/moved-before.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/moved-before.md", ConfirmMove: true}); err != nil {
		t.Fatal(err)
	}
	if sqlite {
		if err := store.MigrateSQLite(s.Dir()); err != nil {
			t.Fatal(err)
		}
	}
	injectLegacyTranslation(t, s.Dir(), sqlite)
	for key, data := range storedRecords(t, s.Dir(), sqlite) {
		if !strings.Contains(data, `"translation`) {
			t.Fatal("legacy key not injected into", key)
		}
	}
	s, err := openStore(s.Dir())
	if err != nil || s.IsSQLite() != sqlite {
		t.Fatal("store with legacy translation keys did not open", err)
	}
	page, err := s.page("from/docs/edit.md", false)
	if err != nil || page.Text != "# Original docs/edit.md" {
		t.Fatal("legacy article not served as its original", err, page.Text)
	}
	if a, err := s.article("handbook", "README.md", false); err != nil || a.Versions[len(a.Versions)-1].Text != "# Initial" {
		t.Fatal("legacy imported article unreadable", err)
	}
	if _, err = s.put("from", "docs/edit.md", "# Edited", 1); err != nil {
		t.Fatal("edit of legacy article failed", err)
	}
	if _, err = s.copyArticle(transfer.CopyRequest{SourceID: "from", SourcePath: "docs/copy.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/copy.md"}); err != nil {
		t.Fatal("copy of legacy article failed", err)
	}
	if _, err = s.moveArticle(transfer.MoveRequest{SourceID: "from", SourcePath: "docs/move.md", ExpectedRevision: 1, DestinationID: "to", DestinationPath: "docs/move.md", ConfirmMove: true}); err != nil {
		t.Fatal("move of legacy article failed", err)
	}
	batch := applyAgain(s, testBatch())
	batch.Files["README.md"] = "# Imported again"
	if _, err = s.importBatch(batch); err != nil {
		t.Fatal("import over legacy article failed", err)
	}
	reopened, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"from/docs/edit.md": "# Edited", "to/docs/copy.md": "# Original docs/copy.md", "to/docs/move.md": "# Original docs/move.md", "handbook/README.md": "# Imported again"} {
		if page, err := reopened.page(key, false); err != nil || page.Text != want {
			t.Fatal("written article lost", key, err)
		}
	}
	// The JSON backend rewrites every record on a commit; SQLite rewrites the
	// records a commit touches, which includes every move.
	written := []string{"from/docs/edit.md", "from/docs/move.md", "to/docs/copy.md", "to/docs/move.md", "handbook/README.md"}
	for key, data := range storedRecords(t, s.Dir(), sqlite) {
		touched := !sqlite || strings.HasPrefix(key, "move/") || slices.Contains(written, strings.TrimPrefix(key, "article/"))
		if touched && strings.Contains(data, `"translation`) {
			t.Fatalf("written record %s kept a translation key: %s", key, data)
		}
	}
}

// injectLegacyTranslation adds the removed keys to every stored article and
// move, as a store written before the removal holds them.
func injectLegacyTranslation(t *testing.T, dir string, sqlite bool) {
	t.Helper()
	if sqlite {
		db := storetest.SQLite(t, dir)
		defer db.Close()
		if _, err := db.Exec(`UPDATE records SET data=json_set(data,'$.translation',json(?)) WHERE category='article'`, legacyTranslation); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE records SET data=json_set(data,'$.translation_fingerprint',?) WHERE category='move'`, legacyFingerprint); err != nil {
			t.Fatal(err)
		}
		return
	}
	p := filepath.Join(dir, "state.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]json.RawMessage
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]string{"articles": "translation", "moves": "translation_fingerprint"} {
		var records map[string]map[string]json.RawMessage
		if err = json.Unmarshal(state[name], &records); err != nil || len(records) == 0 {
			t.Fatal("fixture lacks", name, err)
		}
		value := json.RawMessage(legacyTranslation)
		if key == "translation_fingerprint" {
			value = json.RawMessage(`"` + legacyFingerprint + `"`)
		}
		for _, record := range records {
			record[key] = value
		}
		if state[name], err = json.Marshal(records); err != nil {
			t.Fatal(err)
		}
	}
	if raw, err = json.Marshal(state); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// storedRecords returns the raw stored article and move records by
// "article/<key>" or "move/<id>".
func storedRecords(t *testing.T, dir string, sqlite bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	if sqlite {
		db := storetest.SQLite(t, dir)
		defer db.Close()
		rows, err := db.Query(`SELECT category,id,data FROM records WHERE category IN ('article','move')`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var category, id, data string
			if err = rows.Scan(&category, &id, &data); err != nil {
				t.Fatal(err)
			}
			out[category+"/"+id] = data
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Articles map[string]json.RawMessage `json:"articles"`
		Moves    map[string]json.RawMessage `json:"moves"`
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	for key, data := range state.Articles {
		out["article/"+key] = string(data)
	}
	for id, data := range state.Moves {
		out["move/"+id] = string(data)
	}
	return out
}
