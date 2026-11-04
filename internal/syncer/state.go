package syncer

import (
	"errors"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/github"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// errUnchanged ends a transaction that has nothing to commit.
var errUnchanged = errors.New("unchanged")

// read runs fn under the store read lock. View returns only fn's error, and
// fn cannot fail.
func (m *Manager) read(fn func(db *domain.Database)) {
	_ = m.store.View(func(db *domain.Database) error {
		fn(db)
		return nil
	})
}

// update runs fn in one transaction; fn returns errUnchanged to commit
// nothing and succeed.
func (m *Manager) update(fn func(tx *store.Tx) error) error {
	if err := m.store.Update(fn); !errors.Is(err, errUnchanged) {
		return err
	}
	return nil
}

// Record a changed remote head before fetching it, so the public snapshot of
// the previous head is withheld until that head is imported.
func (m *Manager) observeChangedHead(expected domain.GitHubConnection, head string) error {
	return m.update(func(tx *store.Tx) error {
		db := tx.DB()
		c, ok := db.Connections[expected.ID]
		if tx.StoreInvalid() || !ok || c.Paused || c.ConfigRevision != expected.ConfigRevision || !domain.ValidCommitID(head) {
			return domain.ErrConflict
		}
		if c.PendingHead == head {
			return errUnchanged
		}
		c.PendingHead = head
		db.Connections[c.ID] = c
		tx.OnlyConnections()
		return nil
	})
}

// An unchanged, freshly verified GitHub head confirms the already imported
// snapshot without downloading every blob again. Never accept an observation
// from a request that raced with reconfiguration or a different import.
func (m *Manager) observeGitHubHead(expected domain.GitHubConnection, head, tree string) error {
	// Exclusive is a write-locked read here: it preserves the write-lock span
	// the old code held while it checked and recorded the observation.
	return m.store.Exclusive(func(ex *store.Exclusive) error {
		db := ex.DB()
		c, ok := db.Connections[expected.ID]
		src, exists := db.Sources[expected.ID]
		if ex.Invalid() ||
			!ok ||
			!exists ||
			c.Paused ||
			c.ConfigRevision != expected.ConfigRevision ||
			c.RepositoryID != expected.RepositoryID ||
			c.Branch != expected.Branch ||
			c.Private != expected.Private ||
			c.Verification != "verified" ||
			c.LastPull != head ||
			c.TreeSHA != tree ||
			src.ConnectionID != c.ID ||
			src.ImportCommit != head ||
			(src.Visibility == "private") != c.Private {
			return domain.ErrConflict
		}
		for _, ref := range src.Assets {
			if m.store.VerifyBlob(ref) != nil {
				return importer.ErrAssetUnavailable
			}
		}
		m.importer.MarkObserved(c.ID)
		return nil
	})
}

// RetryConnection schedules the connection and its queued writes at once,
// unless GitHub's rate-limit cooldown is still running.
func (m *Manager) RetryConnection(id string) error {
	return m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		c, ok := db.Connections[id]
		if !ok {
			return domain.ErrNotFound
		}
		if c.Paused {
			return domain.ErrConflict
		}
		if c.LastError == "rate_limited" {
			if due, err := time.Parse(time.RFC3339Nano, c.NextAttempt); err == nil && due.After(time.Now()) {
				return domain.ErrConflict
			}
		}
		c.NextAttempt = ""
		if c.State != "syncing" {
			c.State = "pending"
		}
		db.Connections[id] = c
		for key, j := range db.Outbox {
			if j.SourceID == id {
				j.NextAttempt = ""
				db.Outbox[key] = j
			}
		}
		return nil
	})
}

// sourceSyncState summarizes the pending moves, conflicts and writes of a
// connected source.
func (m *Manager) sourceSyncState(id string) (state, code string) {
	m.read(func(db *domain.Database) { state, code = syncState(db, id) })
	return state, code
}

func syncState(db *domain.Database, id string) (string, string) {
	state, code := "ready", ""
	for _, move := range db.Moves {
		if move.SourceConnectionID == id && (move.State == "waiting_source_delete" || move.State == "deleting_source") {
			state, code = "pending", "move_pending"
		}
	}
	for _, a := range db.Articles {
		if a.SourceID == id {
			for _, c := range a.Conflicts {
				if c.Resolution == "" {
					return "conflict", "source_conflict"
				}
			}
		}
	}
	for _, j := range db.Outbox {
		if j.SourceID != id {
			continue
		}
		switch j.State {
		case "conflict":
			return "conflict", "source_conflict"
		case "blocked":
			state, code = "blocked", j.LastError
		case "pending", "prepared", "retry":
			if state != "blocked" {
				state = "pending"
			}
		}
	}
	return state, code
}

// A skipped commit can be reused only while no local source exists and the
// connection configuration still matches the one used for the remote read.
func (m *Manager) recordSkippedRepository(observed domain.GitHubConnection, head, tree string) error {
	if !domain.ValidCommitID(head) || !domain.ValidCommitID(tree) {
		return domain.ErrInvalid
	}
	return m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		c, ok := db.Connections[observed.ID]
		_, exists := db.Sources[observed.ID]
		if !ok || exists || c.Paused || c.ConfigRevision != observed.ConfigRevision || c.RepositoryID != observed.RepositoryID || c.Branch != observed.Branch || c.Private != observed.Private {
			return domain.ErrConflict
		}
		c.State, c.LastError, c.LastPull, c.TreeSHA = "skipped", "", head, tree
		c.NextAttempt, c.Attempts = "", 0
		db.Connections[c.ID] = c
		return nil
	})
}

// identity returns the GitHub identity whose rate limit a connection using
// accountID consumes: the kind and case-folded login of the account, so two
// account records of one login share it, or anonymous access, which GitHub
// limits per address.
func identity(db *domain.Database, accountID string) string {
	if accountID == "" {
		return "anonymous"
	}
	a, ok := db.Accounts[accountID]
	if !ok {
		return "account\x00" + accountID
	}
	return a.Kind + "\x00" + strings.ToLower(a.Login)
}

// connectionStatusFor records the outcome of a synchronization step of
// observed, unless the connection was paused or reconfigured meanwhile. A
// rate limit puts every connection of the same GitHub identity on cooldown.
func (m *Manager) connectionStatusFor(observed domain.GitHubConnection, state, verification, code string, info *github.RepositoryInfo, retryAt time.Time) error {
	return m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		id := observed.ID
		c, ok := db.Connections[id]
		if !ok {
			return domain.ErrNotFound
		}
		if c.Paused || c.ConfigRevision != observed.ConfigRevision {
			return domain.ErrConflict
		}
		c.State = state
		c.LastError = code
		if verification != "" {
			c.Verification = verification
		}
		if info != nil {
			if c.RepositoryID != info.ID {
				return domain.ErrConflict
			}
			// A newly private repository is hidden at once. Visibility proof for
			// the served snapshot is renewed only after the pull completes.
			c.Private = *info.Private
			if state != "syncing" {
				c.VerifiedAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
		}
		if code != "" {
			c.Attempts++
			delay := time.Duration(1<<min(c.Attempts, 8)) * time.Second
			next := time.Now().Add(delay)
			if code == "rate_limited" && retryAt.After(next) {
				next = retryAt
			}
			c.NextAttempt = next.UTC().Format(time.RFC3339Nano)
		} else {
			c.Attempts = 0
			c.NextAttempt = ""
		}
		db.Connections[id] = c
		if code == "rate_limited" {
			limited := identity(db, c.AccountID)
			for otherID, other := range db.Connections {
				if otherID == id || identity(db, other.AccountID) != limited {
					continue
				}
				previous, _ := time.Parse(time.RFC3339Nano, other.NextAttempt)
				next, _ := time.Parse(time.RFC3339Nano, c.NextAttempt)
				if previous.After(next) && other.LastError == "rate_limited" {
					next = previous
				}
				other.NextAttempt = next.UTC().Format(time.RFC3339Nano)
				other.LastError = "rate_limited"
				other.Verification = "unknown"
				if !other.Paused {
					other.State = "error"
				}
				db.Connections[otherID] = other
			}
		}
		if info != nil && *info.Private {
			if src, found := db.Sources[id]; found && src.Visibility != "private" {
				// Making the source private changes a source record too.
				src.Visibility = "private"
				db.Sources[id] = src
				return nil
			}
		}
		tx.OnlyConnections()
		return nil
	})
}

// setJobFor records the outcome of a write attempt of original, unless the
// connection or the job changed meanwhile.
func (m *Manager) setJobFor(expected domain.GitHubConnection, original domain.SyncJob, state, code, prepared string) error {
	return m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		j, ok := db.Outbox[original.ID]
		if !ok {
			return domain.ErrNotFound
		}
		c, connected := db.Connections[expected.ID]
		if !connected ||
			c.Paused ||
			c.ConfigRevision != expected.ConfigRevision ||
			c.RepositoryID != expected.RepositoryID ||
			c.Branch != expected.Branch ||
			j.SourceID != expected.ID ||
			j.Revision != original.Revision ||
			j.SHA != original.SHA ||
			j.Visibility != original.Visibility ||
			domain.JobTerminal(j.State) ||
			(j.State == "conflict" && state != "conflict") ||
			(j.State != original.State && j.State != "prepared") {
			return domain.ErrConflict
		}
		j.State = state
		j.LastError = code
		if prepared != "" {
			j.PreparedCommit = prepared
		}
		if code != "" {
			j.Attempts++
			j.NextAttempt = time.Now().Add(time.Duration(1<<min(j.Attempts, 8)) * time.Second).UTC().Format(time.RFC3339Nano)
		} else {
			j.NextAttempt = ""
		}
		db.Outbox[original.ID] = j
		return nil
	})
}

// Persist the prepared commit only while the same connection and job remain
// eligible. A concurrent edit may have superseded the job during network I/O.
func (m *Manager) prepareJob(id, commit string, expected domain.GitHubConnection) error {
	return m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		job, ok := db.Outbox[id]
		connection, connected := db.Connections[expected.ID]
		if !ok ||
			!connected ||
			connection.Paused ||
			!connection.Write ||
			connection.ConfigRevision != expected.ConfigRevision ||
			connection.RepositoryID != expected.RepositoryID ||
			connection.Branch != expected.Branch ||
			job.SourceID != expected.ID ||
			domain.JobTerminal(job.State) ||
			job.State == "conflict" {
			return domain.ErrConflict
		}
		if len(job.Bundle) > 0 {
			a, ok := db.Articles[job.SourceID+"/"+job.Path]
			if !ok || a.Deleted || domain.LatestRevision(a) != job.Revision || !domain.LastHasSHA(a, job.SHA) {
				return domain.ErrConflict
			}
			for _, conflict := range a.Conflicts {
				if conflict.Resolution == "" {
					return domain.ErrConflict
				}
			}
			if job.Visibility == "public" && (a.Published == nil || a.Published.SHA != job.SHA) {
				return domain.ErrConflict
			}
			if _, err := bundleContents(*db, job); err != nil {
				return err
			}
			if !domain.LinksPresent(*db, job.SourceID, job.Links) {
				return domain.ErrConflict
			}
		}
		job.State = "prepared"
		job.LastError = ""
		job.NextAttempt = ""
		job.PreparedCommit = commit
		db.Outbox[id] = job
		return nil
	})
}

// PauseConnection pauses or resumes a connection and invalidates every
// synchronization step started under its previous configuration.
func (m *Manager) PauseConnection(id string, paused bool) error {
	return m.store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		if domain.MoveDeletionLocksConnection(*db, id) {
			return domain.ErrConflict
		}
		c, ok := db.Connections[id]
		if !ok {
			return domain.ErrNotFound
		}
		c.Paused = paused
		c.ConfigRevision++
		if paused {
			c.State = "paused"
			c.Verification = "unknown"
		} else {
			c.State = "pending"
			if c.LastError != "rate_limited" {
				c.NextAttempt = ""
			}
		}
		db.Connections[id] = c
		return nil
	})
}
