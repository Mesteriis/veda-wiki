package syncer

import (
	"context"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// adoptionSnapshot captures exactly the local state whose baselines change
// when the source is adopted. The importer compares it again inside the
// adopting transaction.
func (m *Manager) adoptionSnapshot(id string) (string, error) {
	var snapshot string
	// Exclusive only reads here; it keeps the write-lock span the old code held
	// so no commit interleaves with the snapshot.
	err := m.store.Exclusive(func(ex *store.Exclusive) error {
		if ex.Invalid() {
			return domain.ErrConflict
		}
		var err error
		snapshot, err = domain.AdoptionSnapshot(*ex.DB(), id)
		return err
	})
	return snapshot, err
}

// remoteProof binds a pull of commit and tree from c to the connection state
// it was fetched under, and records them with the imported content.
func remoteProof(c domain.GitHubConnection, visibility, commit, treeSHA string) *importer.RemoteProof {
	return &importer.RemoteProof{ConnectionID: c.ID, ConfigRevision: c.ConfigRevision, Visibility: visibility, TreeSHA: treeSHA, Record: func(db *domain.Database) {
		recordPull(db, c.ID, commit, treeSHA)
	}}
}

// adoptionProof attaches c to the source with the same ID when the import
// adopts it.
func adoptionProof(ctx context.Context, c domain.GitHubConnection, account domain.GitHubAccount, snapshot string) *importer.AdoptionProof {
	return &importer.AdoptionProof{Connection: c, Account: account, Snapshot: snapshot, Context: ctx, Attach: func(db *domain.Database) {
		adoptConnection(db, c)
	}}
}

// adoptConnection records conn for the source with the same ID. A local
// checkout's old import base is not a GitHub merge base, so it is cleared:
// differing files become conflicts and absent files stay local.
func adoptConnection(db *domain.Database, conn domain.GitHubConnection) {
	for key, article := range db.Articles {
		if article.SourceID == conn.ID {
			article.UpstreamHash = ""
			db.Articles[key] = article
		}
	}
	db.Connections[conn.ID] = conn
}

// recordPull stores the commit and tree a connection was last imported from.
func recordPull(db *domain.Database, connectionID, commit, treeSHA string) {
	conn := db.Connections[connectionID]
	conn.LastPull = commit
	conn.TreeSHA = treeSHA
	db.Connections[conn.ID] = conn
}
