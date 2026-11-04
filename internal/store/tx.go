package store

import (
	"errors"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// View runs fn under the read lock. fn must not modify db or retain any of
// its maps, slices or pointers after returning.
func (s *Store) View(fn func(db *domain.Database) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(&s.db)
}

type txScope int

const (
	scopeAll txScope = iota
	scopeArticle
	scopeConnections
)

// Tx is a pending change: a private clone of the database and the set of
// SQLite records the commit must write.
type Tx struct {
	db          domain.Database
	scope       txScope
	article     string
	scoped      bool
	invalid     bool
	afterCommit []func()
}

// DB returns the transaction's mutable clone. Replace slices and pointers in
// copied values instead of mutating them in place; they are shared with the
// committed state until the transaction commits.
func (tx *Tx) DB() *domain.Database { return &tx.db }

// StoreInvalid reports whether the store had failed closed when the
// transaction began; its commit will then fail.
func (tx *Tx) StoreInvalid() bool { return tx.invalid }

// AfterCommit registers fn to run under the write lock once the transaction
// has committed successfully. It does not run when fn or the commit fails.
func (tx *Tx) AfterCommit(fn func()) { tx.afterCommit = append(tx.afterCommit, fn) }

// OnCommit registers fn to run under the write lock after every successful
// commit of any transaction. fn must return at once and never take the store
// lock: it is meant for a non-blocking signal (a send to a buffered channel
// with a default case) to background work that then reads with View.
func (s *Store) OnCommit(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitHooks = append(s.commitHooks, fn)
}

// OnlyArticle declares that the transaction changes exactly one article and
// may add or update outbox jobs and move states, but removes neither and
// changes no other record category. SQLite then writes only those records.
func (tx *Tx) OnlyArticle(key string) { tx.narrow(scopeArticle, key) }

// OnlyConnections declares that the transaction changes only GitHub
// connection records and removes none.
func (tx *Tx) OnlyConnections() { tx.narrow(scopeConnections, "") }

// Conflicting declarations fall back to persisting the whole state.
func (tx *Tx) narrow(scope txScope, article string) {
	if tx.scoped && (tx.scope != scope || tx.article != article) {
		tx.scope, tx.article = scopeAll, ""
		return
	}
	tx.scope, tx.article, tx.scoped = scope, article, true
}

// Update runs fn under the write lock with a clone of the database. When fn
// returns nil the clone is committed within the declared scope; otherwise
// neither memory nor disk changes and fn's error is returned.
//
// A failed commit returns its error. On SQLite the store then reloads the
// committed records, or fails closed (Invalid) if that is impossible. On JSON,
// if only the directory fsync after the atomic rename fails, memory has
// already advanced to the new state and the error is still returned.
func (s *Store) Update(fn func(tx *Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(fn)
}

// update is Update for a caller that holds the write lock.
func (s *Store) update(fn func(tx *Tx) error) error {
	if s.readOnly {
		return errReadOnly
	}
	tx := &Tx{db: clone(&s.db), invalid: s.invalid}
	if err := fn(tx); err != nil {
		return err
	}
	tx.prune()
	var err error
	switch tx.scope {
	case scopeArticle:
		err = s.commitArticle(tx.db, tx.article)
	case scopeConnections:
		err = s.commitTargeted(tx.db, s.commitSQLiteConnections)
	default:
		err = s.commitTargeted(tx.db, nil)
	}
	if err != nil {
		return err
	}
	for _, after := range tx.afterCommit {
		after()
	}
	for _, hook := range s.commitHooks {
		hook()
	}
	s.collectBlobsDue()
	return nil
}

// prune applies the revision retention rule (domain.PruneArticle) to the
// final records of the transaction, before they are persisted and published,
// so memory and disk hold the same pruned history. Only articles the commit
// persists are pruned: a commit limited to one article prunes that article
// alone, a connections commit prunes nothing and a whole-state commit prunes
// every article. A protection lifted by a narrow commit of another article
// takes effect at that article's next write or the next whole-state commit.
func (tx *Tx) prune() {
	switch {
	case tx.scope == scopeConnections:
	case tx.scope == scopeArticle && tx.article != "":
		domain.PruneArticle(&tx.db, tx.article)
	default:
		domain.PruneAll(&tx.db)
	}
}

// An empty key persists the whole state. The caller holds the lock.
func (s *Store) commitArticle(db domain.Database, key string) error {
	var targeted func(domain.Database) error
	if key != "" {
		targeted = func(db domain.Database) error { return s.commitSQLiteArticle(db, key) }
	}
	return s.commitTargeted(db, targeted)
}

// commitTargeted persists db and then publishes it in memory. A nil targeted
// writer persists the whole SQLite state. The caller holds the lock.
func (s *Store) commitTargeted(db domain.Database, targeted func(domain.Database) error) error {
	if s.readOnly {
		return errReadOnly
	}
	if s.invalid {
		return errors.New("store unavailable")
	}
	db.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	if !s.sqlite {
		return s.writeJSON(db)
	}
	var err error
	if targeted != nil {
		err = targeted(db)
	} else {
		err = s.commitSQLite(db)
	}
	if err != nil {
		s.recoverSQLite()
		return err
	}
	s.db = db
	return nil
}
