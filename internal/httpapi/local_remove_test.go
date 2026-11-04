package httpapi

import (
	"context"
	"encoding/json"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoveLocalSourceGuardAndSQLite(t *testing.T) {
	s := testStore(t)
	if _, err := s.importBatch(testBatch()); err != nil {
		t.Fatal(err)
	}
	source(t, s, "other", "private")
	if _, err := s.put("other", "keep.md", "# Keep", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	generation := s.state(t).Sources["handbook"].ImportGeneration
	w := Wiki{Store: s, ReaderLocal: true, LocalImportSource: "handbook"}
	body, _ := json.Marshal(map[string]any{"expected_generation": generation, "confirm": true})
	call := func(root bool, origin string, body string) int {
		r := httptest.NewRequest("POST", "http://localhost/api/local/remove-imported-source", strings.NewReader(body))
		if root {
			r = r.WithContext(context.WithValue(r.Context(), LocalRootKey{}, true))
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out.Code
	}
	if call(false, "", string(body)) == 200 || call(true, "http://localhost", string(body)) == 200 {
		t.Fatal("untrusted removal")
	}
	if _, err = s.removeLocalImportedSource("handbook", "wrong"); err != domain.ErrConflict {
		t.Fatal("stale generation accepted", err)
	}
	if _, err = s.auth().SaveTeam("fixture-team", []string{"handbook"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.removeLocalImportedSource("handbook", generation); err != domain.ErrConflict {
		t.Fatal("dependent team ignored", err)
	}
	s.mutate(t, func(db *domain.Database) {
		delete(db.Teams, "fixture-team")
	})
	if call(true, "", string(body)) != 200 {
		t.Fatal("authorized deletion failed")
	}
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.state(t).Sources["handbook"]; ok {
		t.Fatal("source remains")
	}
	if _, ok := s.state(t).Articles["handbook/README.md"]; ok {
		t.Fatal("article remains")
	}
	if _, ok := s.state(t).Articles["other/keep.md"]; !ok {
		t.Fatal("other article removed")
	}
}
