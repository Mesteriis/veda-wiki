package store

import "github.com/Mesteriis/veda-wiki/internal/domain"

// Exclusive is the write lock held across several transactions. Operations
// that must serialize a remote update with the local state it was prepared
// from, or apply a bounded series of commits that no other writer may
// interleave with, read the committed state and commit each step with Update.
type Exclusive struct {
	s *Store
}

// Exclusive runs fn under the write lock without a transaction of its own.
// Each ex.Update inside fn is a separate transaction: its commit stays
// committed even if fn later fails, and an error discards only that
// transaction's changes.
func (s *Store) Exclusive(fn func(ex *Exclusive) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(&Exclusive{s: s})
}

// DB returns the committed state. It changes after each successful Update and
// must not be modified or retained after fn returns.
func (ex *Exclusive) DB() *domain.Database { return &ex.s.db }

// Invalid reports whether the store has failed closed.
func (ex *Exclusive) Invalid() bool { return ex.s.invalid }

// Update runs one transaction under the held lock, as Store.Update does.
func (ex *Exclusive) Update(fn func(tx *Tx) error) error { return ex.s.update(fn) }
