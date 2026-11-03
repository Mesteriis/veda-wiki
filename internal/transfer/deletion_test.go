package transfer

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func TestSourceDeletionPreparesAndRejects(t *testing.T) {
	commit := "0123456789abcdef0123456789abcdef01234567"
	db := domain.Database{Moves: map[string]domain.Move{"m": {ID: "m", State: "waiting_source_delete", SourcePath: "guide.md"}}}
	PrepareSourceDeletion(&db, "m", commit)
	if m := db.Moves["m"]; m.State != "deleting_source" || m.PreparedDeletion != commit || m.SourcePath != "guide.md" {
		t.Fatal("prepared move", m)
	}
	RejectSourceDeletion(&db, "m")
	if m := db.Moves["m"]; m.State != "waiting_source_delete" || m.PreparedDeletion != "" || m.SourcePath != "guide.md" {
		t.Fatal("rejected move", m)
	}
}
