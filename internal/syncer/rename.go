package syncer

import (
	"context"
	"log"
	"strings"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// followRename resolves a connection whose repository name answered with a
// redirect. The repository is read by its stored numeric identity, never by
// following the redirect, and only a rename within the same owner is adopted:
// a transfer to another account, or a private connection that turned public,
// stays in error until the owner reconfigures the connection explicitly.
// The new name is a configuration change: the revision moves on and the
// returned connection carries it. A move that locks the connection is not
// affected, since the identity and branch stay the same; a renamed move
// destination is verified again once its own sync has followed the rename.
func (m *Manager) followRename(ctx context.Context, c domain.GitHubConnection, token string) (domain.GitHubConnection, github.RepositoryInfo, error) {
	moved := &github.RemoteError{Code: "repository_moved"}
	info, err := m.github.RepositoryByID(ctx, c.RepositoryID, token)
	if err != nil {
		log.Printf("github connection %s: repository moved and could not be resolved by id: %s", c.ID, github.ErrorCode(err))
		return c, info, moved
	}
	name, err := domain.RepositoryName(info.FullName)
	if err != nil || info.ID != c.RepositoryID || name == c.Repository || !sameOwner(name, c.Repository) || c.Private && !*info.Private {
		log.Printf("github connection %s: repository moved but its identity, owner or visibility does not match; reconfigure it explicitly", c.ID)
		return c, info, moved
	}
	err = m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		current, ok := db.Connections[c.ID]
		if !ok || current.Paused || current.ConfigRevision != c.ConfigRevision {
			return domain.ErrConflict
		}
		current.Repository = name
		current.ConfigRevision++
		db.Connections[c.ID] = current
		c = current
		return nil
	})
	if err != nil {
		return c, info, err
	}
	log.Printf("github connection %s: repository renamed on GitHub, following it by id", c.ID)
	return c, info, nil
}

// sameOwner reports whether two canonical repository names share the owner.
func sameOwner(a, b string) bool {
	return strings.EqualFold(strings.Split(a, "/")[0], strings.Split(b, "/")[0])
}
