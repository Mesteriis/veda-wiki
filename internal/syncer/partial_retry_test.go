package syncer

import (
	"context"
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github/githubtest"
)

// A partial import is reread once per process at an unchanged head, so a
// VEDA upgrade that stops withholding a file completes the source without a
// new commit; later passes of the same process keep the cheap head check.
func TestPartialImportIsRereadOncePerProcess(t *testing.T) {
	s := testStore(t)
	f := githubtest.New(t, map[string]string{"veda.yaml": metadataFixture, "README.md": "# Readme"})
	m := connectFixture(t, s, f, false)
	if s.db().Sources["handbook"].ImportPartial {
		t.Fatal("complete repository imported as partial")
	}
	markPartial := func() {
		s.mutate(func(db *domain.Database) {
			src := db.Sources["handbook"]
			src.ImportPartial = true
			db.Sources["handbook"] = src
		})
	}
	// Marking the source partial stands for an import made under older
	// withholding rules; the head does not move.
	markPartial()
	m.Once(context.Background())
	if s.db().Sources["handbook"].ImportPartial {
		t.Fatal("partial import was not reread at an unchanged head")
	}
	markPartial()
	m.Once(context.Background())
	if !s.db().Sources["handbook"].ImportPartial {
		t.Fatal("the same process reread an unchanged head twice")
	}
	// A restarted VEDA rereads it once more.
	newSyncManager(s, f.Client()).Once(context.Background())
	if s.db().Sources["handbook"].ImportPartial {
		t.Fatal("a new process did not reread the partial import")
	}
}
