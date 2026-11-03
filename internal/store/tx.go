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
	if s.readOnly {
		return errReadOnly
	}
	tx := &Tx{db: clone(&s.db), invalid: s.invalid}
	if err := fn(tx); err != nil {
		return err
	}
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
	return nil
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
	// Old binaries reject the upgraded store before reading a large revision.
	if db.Version < 13 {
		for _, article := range db.Articles {
			large := article.Published != nil && len(article.Published.Text) > domain.LegacyArticleBytes
			large = large || article.Translation != nil && len(article.Translation.Text) > domain.LegacyArticleBytes
			for _, revision := range article.Versions {
				large = large || len(revision.Text) > domain.LegacyArticleBytes
			}
			for _, conflict := range article.Conflicts {
				large = large || len(conflict.Incoming) > domain.LegacyArticleBytes
			}
			if large {
				db.Version = 13
				break
			}
		}
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
