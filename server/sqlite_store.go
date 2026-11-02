package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// SQLite stores one independently updated record per source, article, account,
// connection, job, move or access key. The in-memory model remains authoritative
// during a request; a transaction persists all affected records before publish.
const sqliteFile = "state.sqlite"

type sqliteMarker struct {
	Version int    `json:"version"`
	Backend string `json:"backend"`
}
type sqliteMeta struct {
	Version int    `json:"version"`
	Updated string `json:"updated_at"`
}

func sqliteDB(dir string) (*sql.DB, error) {
	p := filepath.Join(dir, sqliteFile)
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("SQLite store must be a private regular file")
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=rw"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA journal_mode=DELETE; PRAGMA synchronous=EXTRA; PRAGMA busy_timeout=5000; PRAGMA temp_store=MEMORY"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func initSQLite(dir string) error {
	f, err := os.OpenFile(filepath.Join(dir, sqliteFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	db, err := sqliteDB(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE records (category TEXT NOT NULL, id TEXT NOT NULL, data BLOB NOT NULL, PRIMARY KEY (category,id)) WITHOUT ROWID`)
	return err
}

func (s *Store) commitSQLite(state Database) error {
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
	for id, v := range state.Articles {
		if err = put("article", id, v); err != nil {
			return err
		}
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
func (s *Store) commitSQLiteArticle(state Database, key string) error {
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
	if err = put("article", key, article); err != nil {
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

func (s *Store) loadSQLite(marker []byte) error {
	var m sqliteMarker
	decoder := json.NewDecoder(bytes.NewReader(marker))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&m)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = errors.New("trailing marker data")
		}
	}
	if err != nil || m.Version != 14 || m.Backend != "sqlite" {
		return errors.New("unsupported store marker")
	}
	db, err := sqliteDB(s.dir)
	if err != nil {
		return err
	}
	defer db.Close()
	var integrity string
	if err = db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("SQLite integrity check failed")
	}
	rows, err := db.Query(`SELECT category,id,data FROM records`)
	if err != nil {
		return err
	}
	defer rows.Close()
	result := Database{Sources: map[string]Source{}, Articles: map[string]Article{}, Accounts: map[string]GitHubAccount{}, Connections: map[string]GitHubConnection{}, Outbox: map[string]SyncJob{}, Moves: map[string]Move{}, AccessKeys: map[string]AccessKey{}, Users: map[string]WikiUser{}, Teams: map[string]WikiTeam{}, SourceMirrors: map[string]SourceMirror{}}
	metaFound := false
	for rows.Next() {
		var category, id string
		var data []byte
		if err = rows.Scan(&category, &id, &data); err != nil {
			return err
		}
		switch category {
		case "meta":
			var v sqliteMeta
			if id != "state" || metaFound {
				return errors.New("invalid SQLite metadata")
			}
			err = json.Unmarshal(data, &v)
			result.Version, result.Updated = v.Version, v.Updated
			metaFound = true
		case "source":
			var v Source
			err = json.Unmarshal(data, &v)
			result.Sources[id] = v
		case "article":
			var v Article
			err = json.Unmarshal(data, &v)
			result.Articles[id] = v
		case "account":
			var v GitHubAccount
			err = json.Unmarshal(data, &v)
			result.Accounts[id] = v
		case "connection":
			var v GitHubConnection
			err = json.Unmarshal(data, &v)
			result.Connections[id] = v
		case "job":
			var v SyncJob
			err = json.Unmarshal(data, &v)
			result.Outbox[id] = v
		case "move":
			var v Move
			err = json.Unmarshal(data, &v)
			result.Moves[id] = v
		case "source_mirror":
			var v SourceMirror
			err = json.Unmarshal(data, &v)
			result.SourceMirrors[id] = v
		case "access_key":
			var v AccessKey
			err = json.Unmarshal(data, &v)
			result.AccessKeys[id] = v
		case "user":
			var v WikiUser
			err = json.Unmarshal(data, &v)
			result.Users[id] = v
		case "team":
			var v WikiTeam
			err = json.Unmarshal(data, &v)
			result.Teams[id] = v
		default:
			return errors.New("unknown SQLite record category")
		}
		if err != nil {
			return errors.New("invalid SQLite record")
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if !metaFound {
		return errors.New("missing SQLite metadata")
	}
	s.db = result
	s.sqlite = true
	return nil
}
