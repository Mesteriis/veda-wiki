package syncer

import (
	"context"
	"errors"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

// errMoveWaiting discards a re-evaluation that leaves the move waiting; the
// prepared deletion commits the same evaluation.
var errMoveWaiting = errors.New("move still waiting")

// Serialize local mutations with the remote ref update. Once prepared, the
// stored commit keeps both articles locked until a complete pull resolves it.
func (m *Manager) deletePendingMove(ctx context.Context, c domain.GitHubConnection, head, token string) (bool, error) {
	var firstErr error
	for _, move := range m.transfer.Moves() {
		if move.SourceConnectionID != c.ID || (move.State != "waiting_source_delete" && move.State != "deleting_source") {
			continue
		}
		attempted, err := m.deleteMove(ctx, c, head, token, move)
		if err == nil && attempted {
			return true, nil
		}
		if err == nil {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
		// A request with an uncertain outcome may have advanced this branch.
		// Pull again before attempting another deletion using the same head.
		var current domain.Move
		m.read(func(db *domain.Database) { current = db.Moves[move.ID] })
		if current.State == "deleting_source" {
			return true, err
		}
	}
	return false, firstErr
}

// verifyMoveDestination proves that the destination repository already holds
// the moved article, its images and its linked articles.
func (m *Manager) verifyMoveDestination(ctx context.Context, candidate domain.Move) (int, error) {
	var destination domain.GitHubConnection
	var target domain.Article
	var ok, targetOK bool
	m.read(func(db *domain.Database) {
		destination, ok = db.Connections[candidate.DestinationConnectionID]
		target, targetOK = db.Articles[candidate.DestinationID+"/"+candidate.DestinationPath]
	})
	if !ok || !targetOK || len(target.Versions) != 1 || destination.Paused || destination.RepositoryID != candidate.DestinationRepositoryID || destination.Branch != candidate.DestinationBranch {
		return 0, domain.ErrConflict
	}
	secret, _, err := m.secret(destination.AccountID)
	if err != nil {
		return 0, err
	}
	destinationToken, err := m.github.Token(ctx, secret, destination.Repository, false)
	if err != nil {
		return 0, err
	}
	info, err := m.github.Repository(ctx, destination.Repository, destinationToken)
	if err != nil {
		return 0, err
	}
	if info.ID != destination.RepositoryID || *info.Private != destination.Private {
		return 0, domain.ErrConflict
	}
	destinationHead, err := m.github.Head(ctx, destination.Repository, destination.Branch, destinationToken)
	if err != nil {
		return 0, err
	}
	destinationTree, err := m.github.Tree(ctx, destination.Repository, destinationHead, destinationToken)
	if err != nil {
		return 0, err
	}
	if _, err = github.VerifiedTreeFile(destinationTree, candidate.DestinationPath, target.Versions[0].Text); err != nil {
		return 0, err
	}
	for name, ref := range candidate.Assets {
		data, err := m.readAsset(ref)
		if err != nil {
			return 0, err
		}
		if _, err = github.VerifiedTreeFile(destinationTree, name, string(data)); err != nil {
			return 0, err
		}
	}
	linked, err := m.transfer.LinkContents(candidate.DestinationID, candidate.Links)
	if err != nil {
		return 0, err
	}
	for name, text := range linked {
		if _, err = github.VerifiedTreeFile(destinationTree, name, text); err != nil {
			return 0, err
		}
	}
	return destination.ConfigRevision, nil
}

// deleteMove deletes the source article of candidate from GitHub. The store
// write lock is held from the final local check through the ref update, so no
// local change can interleave with the prepared deletion.
func (m *Manager) deleteMove(ctx context.Context, c domain.GitHubConnection, head, token string, candidate domain.Move) (bool, error) {
	var destinationRevision int
	if candidate.DestinationConnectionID != "" {
		revision, err := m.verifyMoveDestination(ctx, candidate)
		if err != nil {
			return true, err
		}
		destinationRevision = revision
	}
	tree, err := m.github.Tree(ctx, c.Repository, head, token)
	if err != nil {
		return true, err
	}
	attempted := false
	err = m.store.Exclusive(func(ex *store.Exclusive) error {
		db := ex.DB()
		current, ok := db.Connections[c.ID]
		if !ok || current.Paused || !current.Write || current.ConfigRevision != c.ConfigRevision || current.RepositoryID != c.RepositoryID || current.Branch != c.Branch {
			attempted = true
			return domain.ErrConflict
		}
		if candidate.DestinationConnectionID != "" {
			destination, ok := db.Connections[candidate.DestinationConnectionID]
			if !ok || destination.Paused || destination.ConfigRevision != destinationRevision {
				attempted = true
				return domain.ErrConflict
			}
		}
		move, ok := db.Moves[candidate.ID]
		if !ok || move.SourceConnectionID != c.ID || (move.State != "waiting_source_delete" && move.State != "deleting_source") {
			return nil
		}
		attempted = true
		return m.deleteSource(ctx, ex, c, head, token, tree, move.ID)
	})
	return attempted, err
}

// deleteSource re-evaluates the complete local invariants of move id and
// sends the source deletion. The caller holds the store write lock, so the
// state evaluated here is still the committed state when GitHub asks to
// prepare the ref update.
func (m *Manager) deleteSource(ctx context.Context, ex *store.Exclusive, c domain.GitHubConnection, head, token string, tree github.Tree, id string) error {
	var move domain.Move
	var reconciled domain.Database
	err := ex.Update(func(tx *store.Tx) error {
		domain.ReconcileMoves(tx.DB())
		move = tx.DB().Moves[id]
		if move.State != "waiting_source_delete" && move.State != "deleting_source" {
			return nil
		}
		reconciled = *tx.DB()
		return errMoveWaiting
	})
	if !errors.Is(err, errMoveWaiting) {
		return err
	}
	if err := m.store.VerifyBlobs(move.Assets); err != nil {
		return err
	}
	original := reconciled.Articles[move.SourceID+"/"+move.SourcePath]
	previousState := move.State
	prepared := false
	prepare := func(commit string) error {
		if err := ex.Update(func(tx *store.Tx) error {
			// Commit the evaluated state, never a second evaluation.
			*tx.DB() = reconciled
			transfer.PrepareSourceDeletion(tx.DB(), id, commit)
			return nil
		}); err != nil {
			return err
		}
		prepared = true
		return nil
	}
	_, err = m.github.Remove(ctx, c.Repository, c.Branch, head, tree, move.SourcePath, original.Versions[move.SourceRevision-1].Text, token, prepare)
	var remote *github.RemoteError
	// A definitive rejection of the first ref update cannot have changed the
	// branch. Do not clear an earlier uncertain attempt on a later rejection.
	if prepared && previousState == "waiting_source_delete" && errors.As(err, &remote) && (remote.Status == 409 || remote.Status == 422) {
		if commitErr := ex.Update(func(tx *store.Tx) error {
			transfer.RejectSourceDeletion(tx.DB(), id)
			return nil
		}); commitErr != nil {
			return commitErr
		}
	}
	if err == nil {
		m.Wake()
	}
	return err
}
