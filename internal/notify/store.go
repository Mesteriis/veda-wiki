// Package notify keeps owner notifications apart from VEDA's state: the
// access requests visitors send from the public surface, the Telegram IDs of
// users, which notifications were already sent, and an outbound-only Telegram
// bot. Its data lives in <data>/notify.sqlite with its own migrations, so the
// main store and its format never change and an older binary still opens it.
package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	_ "modernc.org/sqlite"
)

// File is the database file name in the data directory.
const File = "notify.sqlite"

// migrations are applied in order, each in one transaction. Never edit a
// released migration; append a new one.
var migrations = []string{
	`CREATE TABLE requests (
		id TEXT PRIMARY KEY,
		sources TEXT NOT NULL,
		telegram TEXT NOT NULL,
		comment TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('pending','approved','rejected')),
		key_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		decided_at INTEGER NOT NULL DEFAULT 0
	) WITHOUT ROWID;
	CREATE INDEX requests_state ON requests (state, created_at);
	CREATE TABLE telegram_ids (
		user_id TEXT PRIMARY KEY,
		chat_id INTEGER NOT NULL
	) WITHOUT ROWID;
	CREATE TABLE settings (
		name TEXT PRIMARY KEY,
		value TEXT NOT NULL
	) WITHOUT ROWID;
	CREATE TABLE sent (
		kind TEXT NOT NULL,
		subject TEXT NOT NULL,
		sent_at INTEGER NOT NULL,
		PRIMARY KEY (kind, subject)
	) WITHOUT ROWID;`,
}

// Store is the open notification database.
type Store struct {
	db  *sql.DB
	dir string
}

// Open opens or creates dir/notify.sqlite as a private regular file. A
// database migrated by a newer VEDA is refused.
func Open(dir string) (*Store, error) {
	p := filepath.Join(dir, File)
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
		if err = f.Close(); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("notification database must be a private regular file")
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=rw"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA journal_mode=DELETE; PRAGMA synchronous=EXTRA; PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, dir: dir}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	var current int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM migrations`).Scan(&current); err != nil {
		return err
	}
	if current > len(migrations) {
		return fmt.Errorf("notification database version %d is newer than this VEDA (%d)", current, len(migrations))
	}
	for v := current + 1; v <= len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(migrations[v-1]); err == nil {
			_, err = tx.Exec(`INSERT INTO migrations (version, applied_at) VALUES (?, ?)`, v, time.Now().UnixMilli())
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("notification database migration %d: %w", v, err)
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Request is one visitor's request for API/MCP access to public sources.
type Request struct {
	ID        string   `json:"id"`
	Sources   []string `json:"sources"`
	Telegram  string   `json:"telegram"`
	Comment   string   `json:"comment"`
	State     string   `json:"state"`
	KeyID     string   `json:"key_id,omitempty"`
	CreatedAt string   `json:"created_at"`
	DecidedAt string   `json:"decided_at,omitempty"`
}

// MaxPending bounds the requests waiting for a decision.
const MaxPending = 100

// ErrQueueFull rejects a request while MaxPending requests wait.
var ErrQueueFull = errors.New("too many pending requests")

// AddRequest stores a pending request; the caller has validated it.
func (s *Store) AddRequest(ctx context.Context, r Request, now time.Time) (Request, error) {
	sources, err := json.Marshal(r.Sources)
	if err != nil {
		return Request{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Request{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM requests WHERE state='pending'`).Scan(&pending); err != nil {
		return Request{}, err
	}
	if pending >= MaxPending {
		return Request{}, ErrQueueFull
	}
	r.State, r.CreatedAt = "pending", now.UTC().Format(time.RFC3339)
	if _, err = tx.ExecContext(ctx, `INSERT INTO requests (id, sources, telegram, comment, state, created_at) VALUES (?, ?, ?, ?, 'pending', ?)`,
		r.ID, string(sources), r.Telegram, r.Comment, now.UnixMilli()); err != nil {
		return Request{}, err
	}
	return r, tx.Commit()
}

// Requests lists requests, pending first, newest first within each state.
func (s *Store) Requests(ctx context.Context) ([]Request, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, sources, telegram, comment, state, key_id, created_at, decided_at FROM requests
		ORDER BY state='pending' DESC, created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		var r Request
		var sources string
		var created, decided int64
		if err = rows.Scan(&r.ID, &sources, &r.Telegram, &r.Comment, &r.State, &r.KeyID, &created, &decided); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(sources), &r.Sources) != nil {
			return nil, errors.New("invalid stored request")
		}
		r.CreatedAt = time.UnixMilli(created).UTC().Format(time.RFC3339)
		if decided > 0 {
			r.DecidedAt = time.UnixMilli(decided).UTC().Format(time.RFC3339)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Request returns one request.
func (s *Store) Request(ctx context.Context, id string) (Request, error) {
	all, err := s.Requests(ctx)
	if err != nil {
		return Request{}, err
	}
	for _, r := range all {
		if r.ID == id {
			return r, nil
		}
	}
	return Request{}, domain.ErrNotFound
}

// Decide marks a pending request approved (with the issued key) or rejected.
func (s *Store) Decide(ctx context.Context, id, state, keyID string, now time.Time) error {
	if state != "approved" && state != "rejected" {
		return domain.ErrInvalid
	}
	res, err := s.db.ExecContext(ctx, `UPDATE requests SET state=?, key_id=?, decided_at=? WHERE id=? AND state='pending'`, state, keyID, now.UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return domain.ErrConflict
	}
	return nil
}

// TelegramID is the chat of a user, or 0.
func (s *Store) TelegramID(ctx context.Context, userID string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT chat_id FROM telegram_ids WHERE user_id=?`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// SetTelegramID sets or, with 0, removes a user's chat.
func (s *Store) SetTelegramID(ctx context.Context, userID string, chatID int64) error {
	if chatID == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM telegram_ids WHERE user_id=?`, userID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO telegram_ids (user_id, chat_id) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET chat_id=excluded.chat_id`, userID, chatID)
	return err
}

// TelegramIDs maps user IDs to chats.
func (s *Store) TelegramIDs(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, chat_id FROM telegram_ids`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var user string
		var chat int64
		if err = rows.Scan(&user, &chat); err != nil {
			return nil, err
		}
		out[user] = chat
	}
	return out, rows.Err()
}

// Setting reads an owner setting; missing settings are "".
func (s *Store) Setting(ctx context.Context, name string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE name=?`, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores an owner setting.
func (s *Store) SetSetting(ctx context.Context, name, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (name, value) VALUES (?, ?) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, name, value)
	return err
}

// Mark records that a notification about subject was sent; it reports
// whether it was new.
func (s *Store) Mark(ctx context.Context, kind, subject string, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO sent (kind, subject, sent_at) VALUES (?, ?, ?)`, kind, subject, now.UnixMilli())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Unmark forgets a sent notification; it reports whether one was recorded.
func (s *Store) Unmark(ctx context.Context, kind, subject string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sent WHERE kind=? AND subject=?`, kind, subject)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
