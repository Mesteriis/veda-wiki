package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	_ "modernc.org/sqlite"
)

// SQLite stores one independently updated record per source, article, account,
// connection, job, move or access key. The in-memory model remains authoritative
// during a request; a transaction persists all affected records before publish.
const SQLiteFile = "state.sqlite"

type sqliteMarker struct {
	Version int    `json:"version"`
	Backend string `json:"backend"`
}
type sqliteMeta struct {
	Version int    `json:"version"`
	Updated string `json:"updated_at"`
}

func sqliteDB(dir string) (*sql.DB, error) {
	p := filepath.Join(dir, SQLiteFile)
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
	f, err := os.OpenFile(filepath.Join(dir, SQLiteFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
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
	result := domain.Database{Sources: map[string]domain.Source{}, Articles: map[string]domain.Article{}, Accounts: map[string]domain.GitHubAccount{}, Connections: map[string]domain.GitHubConnection{}, Outbox: map[string]domain.SyncJob{}, Moves: map[string]domain.Move{}, AccessKeys: map[string]domain.AccessKey{}, Users: map[string]domain.WikiUser{}, Teams: map[string]domain.WikiTeam{}, SourceMirrors: map[string]domain.SourceMirror{}}
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
			var v domain.Source
			err = json.Unmarshal(data, &v)
			result.Sources[id] = v
		case "article":
			var v domain.Article
			err = json.Unmarshal(data, &v)
			result.Articles[id] = v
		case "account":
			var v domain.GitHubAccount
			err = json.Unmarshal(data, &v)
			result.Accounts[id] = v
		case "connection":
			var v domain.GitHubConnection
			err = json.Unmarshal(data, &v)
			result.Connections[id] = v
		case "job":
			var v domain.SyncJob
			err = json.Unmarshal(data, &v)
			result.Outbox[id] = v
		case "move":
			var v domain.Move
			err = json.Unmarshal(data, &v)
			result.Moves[id] = v
		case "source_mirror":
			var v domain.SourceMirror
			err = json.Unmarshal(data, &v)
			result.SourceMirrors[id] = v
		case "access_key":
			var v domain.AccessKey
			err = json.Unmarshal(data, &v)
			result.AccessKeys[id] = v
		case "user":
			var v domain.WikiUser
			err = json.Unmarshal(data, &v)
			result.Users[id] = v
		case "team":
			var v domain.WikiTeam
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

// Commit errors can be ambiguous. Reload the authoritative database; if
// recovery fails, fail closed until the process is restarted.
func (s *Store) recoverSQLite() {
	recovered := &Store{dir: s.dir}
	if recovered.loadSQLite([]byte(`{"version":14,"backend":"sqlite"}`)) != nil {
		s.invalid = true
	} else if Validate(&recovered.db) != nil {
		s.invalid = true
	} else {
		s.db = recovered.db
	}
}
