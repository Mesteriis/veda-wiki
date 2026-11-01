package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// AccessKey grants current, read-only content from explicitly named sources.
// The bearer value is returned once and never stored, only its SHA-256 hash.
type AccessKey struct {
	ID      string   `json:"id"`
	Hash    string   `json:"hash"`
	Sources []string `json:"sources"`
	Expires string   `json:"expires_at"`
}

type AccessKeyView struct {
	ID      string   `json:"id"`
	Sources []string `json:"sources"`
	Expires string   `json:"expires_at"`
}

func (s *Store) listAccessKeys() []AccessKeyView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]AccessKeyView, 0, len(s.db.AccessKeys))
	for _, key := range s.db.AccessKeys {
		keys = append(keys, AccessKeyView{ID: key.ID, Sources: append([]string(nil), key.Sources...), Expires: key.Expires})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	return keys
}

func validateAccessKey(id string, key AccessKey, sources map[string]Source) error {
	if !identifier.MatchString(id) || key.ID != id || len(key.Hash) != 64 || len(key.Sources) == 0 || len(key.Sources) > 100 {
		return errors.New("invalid stored access key")
	}
	if _, err := hex.DecodeString(key.Hash); err != nil {
		return errors.New("invalid stored access key")
	}
	if _, err := time.Parse(time.RFC3339Nano, key.Expires); err != nil {
		return errors.New("invalid stored access key")
	}
	for i, source := range key.Sources {
		if _, ok := sources[source]; !ok || (i > 0 && key.Sources[i-1] >= source) {
			return errors.New("invalid stored access key scope")
		}
	}
	return nil
}

func (s *Store) createAccessKey(id string, sourceIDs []string, expiry time.Time) (string, error) {
	if !identifier.MatchString(id) || len(sourceIDs) == 0 || len(sourceIDs) > 100 || !expiry.After(time.Now()) || expiry.After(time.Now().Add(366*24*time.Hour)) {
		return "", ErrInvalid
	}
	sources := append([]string(nil), sourceIDs...)
	sort.Strings(sources)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.db.AccessKeys[id]; ok {
		return "", ErrConflict
	}
	for i, source := range sources {
		if _, ok := s.db.Sources[source]; !ok || (i > 0 && sources[i-1] == source) {
			return "", ErrInvalid
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	token := hex.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	db := s.clone()
	if db.AccessKeys == nil {
		db.AccessKeys = map[string]AccessKey{}
	}
	db.AccessKeys[id] = AccessKey{ID: id, Hash: hex.EncodeToString(digest[:]), Sources: sources, Expires: expiry.UTC().Format(time.RFC3339Nano)}
	if db.Version < 4 {
		db.Version = 4
	}
	if err := s.commit(db); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) revokeAccessKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.db.AccessKeys[id]; !ok {
		return ErrNotFound
	}
	db := s.clone()
	delete(db.AccessKeys, id)
	return s.commit(db)
}

func (s *Store) accessScope(token string, now time.Time) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.accessScopeLocked(token, now)
}

func (s *Store) accessScopeLocked(token string, now time.Time) []string {
	if len(token) != 64 {
		return nil
	}
	if _, err := hex.DecodeString(token); err != nil {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	return s.accessScopeHashLocked(sum, now)
}

func (s *Store) accessScopeHashLocked(sum [32]byte, now time.Time) []string {
	for _, key := range s.db.AccessKeys {
		hash, err := hex.DecodeString(key.Hash)
		expires, expiryErr := time.Parse(time.RFC3339Nano, key.Expires)
		if err == nil && expiryErr == nil && expires.After(now) && subtle.ConstantTimeCompare(sum[:], hash) == 1 {
			return append([]string(nil), key.Sources...)
		}
	}
	return nil
}

func (s *Store) validScopeHash(hash [32]byte, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.accessScopeHashLocked(hash, now) != nil
}

// Build an immutable request-local snapshot under the same lock as key
// verification. Every existing reader surface then operates only on that
// snapshot; no caller-provided source selector can widen it.
func (s *Store) scopedView(token string, now time.Time) (*Store, bool, error) {
	if len(token) != 64 {
		return nil, false, nil
	}
	if _, err := hex.DecodeString(token); err != nil {
		return nil, false, nil
	}
	return s.scopedViewHash(sha256.Sum256([]byte(token)), now)
}

func (s *Store) scopedViewHash(hash [32]byte, now time.Time) (*Store, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	scope := s.accessScopeHashLocked(hash, now)
	if scope == nil {
		return nil, false, nil
	}
	selected := make(map[string]bool, len(scope))
	db := Database{Version: s.db.Version, Sources: map[string]Source{}, Articles: map[string]Article{}, Connections: map[string]GitHubConnection{}}
	observed := map[string]time.Time{}
	for _, id := range scope {
		selected[id] = true
		if source, ok := s.db.Sources[id]; ok {
			db.Sources[id] = source
			if conn, ok := s.db.Connections[source.ConnectionID]; ok {
				db.Connections[conn.ID] = conn
			}
		}
		if t, ok := s.observed[id]; ok {
			observed[id] = t
		}
	}
	for key, article := range s.db.Articles {
		if selected[article.SourceID] {
			db.Articles[key] = article
		}
	}
	encoded, err := json.Marshal(db)
	if err != nil {
		return nil, true, err
	}
	var snapshot Database
	if err = json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, true, err
	}
	return &Store{dir: s.dir, db: snapshot, observed: observed}, true, nil
}
