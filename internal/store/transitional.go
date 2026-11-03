// Transitional low-level access for code not yet moved into its owning package.
// Task 15 deletes this file; new code uses View and Update.

package store

import (
	"sync"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func (s *Store) Lock()    { s.mu.Lock() }
func (s *Store) Unlock()  { s.mu.Unlock() }
func (s *Store) RLock()   { s.mu.RLock() }
func (s *Store) RUnlock() { s.mu.RUnlock() }

// DB returns the live state. It is valid only while the caller holds the lock
// and must be modified only through Commit, except on a Detached store.
func (s *Store) DB() *domain.Database { return &s.db }

// Clone copies the live state for a pending change. The caller holds the lock.
func (s *Store) Clone() domain.Database { return clone(&s.db) }

// Commit persists the whole state. The caller holds the write lock.
func (s *Store) Commit(db domain.Database) error { return s.commitTargeted(db, nil) }

// CommitArticle is Tx.OnlyArticle for a manual commit; an empty key commits
// the whole state. The caller holds the write lock.
func (s *Store) CommitArticle(db domain.Database, key string) error {
	return s.commitArticle(db, key)
}

// CommitConnections is Tx.OnlyConnections for a manual commit. The caller
// holds the write lock.
func (s *Store) CommitConnections(db domain.Database) error {
	return s.commitTargeted(db, s.commitSQLiteConnections)
}

// InvalidLocked is Invalid for a caller that already holds the lock.
func (s *Store) InvalidLocked() bool { return s.invalid }

// ScopedLocked is Scoped for a caller that already holds the read lock.
func (s *Store) ScopedLocked(sources []string) *Store { return s.scopedLocked(sources) }

// Detached wraps an uncommitted candidate state for code that runs store
// methods over it. It never commits.
func Detached(dir string, db domain.Database) *Store {
	return &Store{dir: dir, db: db, readOnly: true}
}

// The import observation accessors below require the caller to hold the lock.

func (s *Store) Observed(sourceID string) (time.Time, bool) {
	t, ok := s.observed[sourceID]
	return t, ok
}

func (s *Store) Withheld(sourceID string) (int, bool) {
	n, ok := s.withheld[sourceID]
	return n, ok
}

func (s *Store) MarkObserved(sourceID string, at time.Time) {
	if s.observed == nil {
		s.observed = map[string]time.Time{}
	}
	s.observed[sourceID] = at
}

func (s *Store) MarkWithheld(sourceID string, count int) {
	if s.withheld == nil {
		s.withheld = map[string]int{}
	}
	s.withheld[sourceID] = count
}

func (s *Store) ForgetImportObservation(sourceID string) {
	delete(s.observed, sourceID)
	delete(s.withheld, sourceID)
}

// PasswordMutex serializes password hashing; Task 6 moves it to auth.
func (s *Store) PasswordMutex() *sync.Mutex { return &s.passwordMu }
