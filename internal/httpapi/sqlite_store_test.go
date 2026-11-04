package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

func TestSQLiteCommitFailureFailsClosed(t *testing.T) {
	s := testStore(t)
	source(t, s, "local", "private")
	if err := store.MigrateSQLite(s.Dir()); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	db := storetest.SQLite(t, s.Dir())
	if _, err = db.Exec("DROP TABLE records"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err = s.put("local", "guide.md", "# Update", 0); err == nil {
		t.Fatal("failed commit accepted")
	}
	out := httptest.NewRecorder()
	Wiki{Store: s}.ServeHTTP(out, httptest.NewRequest("GET", "/health", nil))
	if out.Code != 503 {
		t.Fatal("invalid store remained available", out.Code)
	}
	if _, err = s.put("local", "another.md", "# Update", 0); err == nil {
		t.Fatal("failed store accepted another write")
	}
}
