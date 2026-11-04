package transfer

import "github.com/Mesteriis/veda-wiki/internal/domain"

// PrepareSourceDeletion records that the source deletion of move id is about
// to be sent as commit. The prepared move keeps both articles locked until a
// complete pull of the source repository settles it.
func PrepareSourceDeletion(db *domain.Database, id, commit string) {
	m := db.Moves[id]
	m.State, m.PreparedDeletion = "deleting_source", commit
	db.Moves[id] = m
}

// RejectSourceDeletion returns move id to waiting for its source deletion
// after GitHub definitively rejected the first prepared ref update, which
// therefore cannot have changed the branch.
func RejectSourceDeletion(db *domain.Database, id string) {
	m := db.Moves[id]
	m.State, m.PreparedDeletion = "waiting_source_delete", ""
	db.Moves[id] = m
}
