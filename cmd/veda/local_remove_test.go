package main

import (
	"context"
	"encoding/json"
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
	if err := migrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	generation := s.DB().Sources["handbook"].ImportGeneration
	w := Wiki{Store: s, ReaderLocal: true, LocalImportSource: "handbook"}
	body, _ := json.Marshal(map[string]any{"expected_generation": generation, "confirm": true})
	call := func(root bool, origin string, body string) int {
		r := httptest.NewRequest("POST", "http://localhost/api/local/remove-imported-source", strings.NewReader(body))
		if root {
			r = r.WithContext(context.WithValue(r.Context(), localRootKey{}, true))
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
	if _, err = s.removeLocalImportedSource("handbook", "wrong"); err != ErrConflict {
		t.Fatal("stale generation accepted", err)
	}
	if _, err = s.auth().SaveTeam("fixture-team", []string{"handbook"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.removeLocalImportedSource("handbook", generation); err != ErrConflict {
		t.Fatal("dependent team ignored", err)
	}
	s.Lock()
	db := s.Clone()
	delete(db.Teams, "fixture-team")
	err = s.Commit(db)
	s.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if call(true, "", string(body)) != 200 {
		t.Fatal("authorized deletion failed")
	}
	s, err = openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.DB().Sources["handbook"]; ok {
		t.Fatal("source remains")
	}
	if _, ok := s.DB().Articles["handbook/README.md"]; ok {
		t.Fatal("article remains")
	}
	if _, ok := s.DB().Articles["other/keep.md"]; !ok {
		t.Fatal("other article removed")
	}
}
