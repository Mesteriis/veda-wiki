package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

func (s *Store) commitSQLite(state domain.Database) error {
	db, err := sqliteDB(s.dir)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TEMP TABLE seen (category TEXT NOT NULL, id TEXT NOT NULL, PRIMARY KEY (category,id)) WITHOUT ROWID`); err != nil {
		return err
	}
	save, err := tx.Prepare(`INSERT INTO records(category,id,data) VALUES(?,?,?) ON CONFLICT(category,id) DO UPDATE SET data=excluded.data WHERE records.data<>excluded.data`)
	if err != nil {
		return err
	}
	defer save.Close()
	seen, err := tx.Prepare(`INSERT INTO seen(category,id) VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer seen.Close()
	put := func(category, id string, v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err = save.Exec(category, id, data); err != nil {
			return err
		}
		_, err = seen.Exec(category, id)
		return err
	}
	if err = put("meta", "state", sqliteMeta{state.Version, state.Updated}); err != nil {
		return err
	}
	for id, v := range state.Sources {
		if err = put("source", id, v); err != nil {
			return err
		}
	}
	if err = putArticles(tx, state, put); err != nil {
		return err
	}
	for id, v := range state.Accounts {
		if err = put("account", id, v); err != nil {
			return err
		}
	}
	for id, v := range state.Connections {
		if err = put("connection", id, v); err != nil {
			return err
		}
	}
	for id, v := range state.Outbox {
		if err = put("job", id, v); err != nil {
			return err
		}
	}
	for id, v := range state.Moves {
		if err = put("move", id, v); err != nil {
			return err
		}
	}
	for id, v := range state.SourceMirrors {
		if err = put("source_mirror", id, v); err != nil {
			return err
		}
	}
	for id, v := range state.AccessKeys {
		if err = put("access_key", id, v); err != nil {
			return err
		}
	}
	for id, v := range state.Users {
		if err = put("user", id, v); err != nil {
			return err
		}
	}
	for id, v := range state.Teams {
		if err = put("team", id, v); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM records WHERE NOT EXISTS (SELECT 1 FROM seen WHERE seen.category=records.category AND seen.id=records.id)`); err != nil {
		return err
	}
	return tx.Commit()
}

// put changes exactly one article, and may add or update outbox jobs and move
// states. It does not remove jobs or moves or modify other record categories.
// Keep the affected records in the same durable transaction as the metadata.
func (s *Store) commitSQLiteArticle(state domain.Database, key string) error {
	article, ok := state.Articles[key]
	if !ok {
		return errors.New("missing article in SQLite update")
	}
	db, err := sqliteDB(s.dir)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	save, err := tx.Prepare(`INSERT INTO records(category,id,data) VALUES(?,?,?) ON CONFLICT(category,id) DO UPDATE SET data=excluded.data WHERE records.data<>excluded.data`)
	if err != nil {
		return err
	}
	defer save.Close()
	put := func(category, id string, value any) error {
		data, e := json.Marshal(value)
		if e != nil {
			return e
		}
		_, e = save.Exec(category, id, data)
		return e
	}
	if err = put("meta", "state", sqliteMeta{state.Version, state.Updated}); err != nil {
		return err
	}
	if err = requireRevisionRows(tx, state.Version); err != nil {
		return err
	}
	w, err := newRevisionWriter(tx)
	if err != nil {
		return err
	}
	defer w.close()
	old, err := w.storedHeader(key)
	if err == nil {
		err = w.sync(old, &article)
	}
	if err == nil {
		err = put("article", key, articleHeader(article))
	}
	if err != nil {
		return err
	}
	for id, job := range state.Outbox {
		if err = put("job", id, job); err != nil {
			return err
		}
	}
	for id, move := range state.Moves {
		if err = put("move", id, move); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// commitSQLiteConnections writes the metadata and every connection record. The
// caller changes connections only and never removes one.
func (s *Store) commitSQLiteConnections(state domain.Database) error {
	db, err := sqliteDB(s.dir)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	save, err := tx.Prepare(`INSERT INTO records(category,id,data) VALUES(?,?,?) ON CONFLICT(category,id) DO UPDATE SET data=excluded.data WHERE records.data<>excluded.data`)
	if err != nil {
		return err
	}
	defer save.Close()
	put := func(category, id string, value any) error {
		data, e := json.Marshal(value)
		if e != nil {
			return e
		}
		_, e = save.Exec(category, id, data)
		return e
	}
	if err = put("meta", "state", sqliteMeta{state.Version, state.Updated}); err != nil {
		return err
	}
	for id, c := range state.Connections {
		if err = put("connection", id, c); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// requireRevisionRows refuses a commit unless the file has the revision
// tables of format 21. Files below format 21 are only read, by the
// migration (MigrateRevisions); no commit writes the legacy layout.
func requireRevisionRows(tx *sql.Tx, version int) error {
	rows, err := revisionRowsLayout(tx, version)
	if err != nil {
		return err
	}
	if !rows {
		return errSQLiteLayout
	}
	return nil
}

// putArticles writes every article record of a whole-state commit: headers
// and the revision difference, and it removes the rows of articles the state
// no longer holds; their records are removed with every other unseen record.
func putArticles(tx *sql.Tx, state domain.Database, put func(category, id string, v any) error) error {
	if err := requireRevisionRows(tx, state.Version); err != nil {
		return err
	}
	w, err := newRevisionWriter(tx)
	if err != nil {
		return err
	}
	defer w.close()
	stored, err := w.storedHeaders()
	if err != nil {
		return err
	}
	for id, v := range state.Articles {
		var old *domain.Article
		if header, ok := stored[id]; ok {
			old = &header
			delete(stored, id)
		}
		if err = w.sync(old, &v); err != nil {
			return err
		}
		if err = put("article", id, articleHeader(v)); err != nil {
			return err
		}
	}
	for _, header := range stored {
		if err = w.sync(&header, nil); err != nil {
			return err
		}
	}
	return nil
}
