package store_test

import (
	"os"
	"testing"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/store/storetest"
)

// compatText is the revision scripts/smoke_store_compat.py expects the
// current binary to serve.
const compatText = "# Synthetic guide\n\nThird revision from the new store."

// Writes a revision through Update into a copy of the fc35bea fixture that
// "veda migrate-revisions" converted, so that scripts/smoke_store_compat.py
// can check that the deployed binary refuses it and the current one serves
// the new revision.
func TestWriteForPreviousBinary(t *testing.T) {
	dir := os.Getenv("VEDA_STORE_COMPAT_DIR")
	if dir == "" {
		t.Skip("compatibility directory not provided")
	}
	st, err := store.Open(dir)
	if err != nil || !st.IsSQLite() {
		t.Fatal("cannot open the converted fixture copy as SQLite", err)
	}
	const key = "fixture/guide.md"
	if err = st.Update(func(tx *store.Tx) error {
		a := tx.DB().Articles[key]
		if len(a.Versions) != 2 {
			return domain.ErrConflict
		}
		a.Versions = append(append([]domain.Revision(nil), a.Versions...), domain.Revision{Number: 3, Text: compatText, SHA: domain.Digest([]byte(compatText)), Created: time.Now().UTC().Format(time.RFC3339Nano)})
		tx.DB().Articles[key] = a
		tx.OnlyArticle(key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a := storetest.Read(t, reopened).Articles[key]; len(a.Versions) != 3 || a.Versions[2].Text != compatText {
		t.Fatal("revision not durable")
	}
}
