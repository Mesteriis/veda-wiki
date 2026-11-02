package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"
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
	ETag    string   `json:"etag,omitempty"`
}

func accessKeyView(key AccessKey) AccessKeyView {
	// The bearer is random; only its stored digest enters this opaque fingerprint.
	// Including it distinguishes a revoked/reissued key with the same metadata.
	encoded, _ := json.Marshal(key)
	digest := sha256.Sum256(encoded)
	return AccessKeyView{ID: key.ID, Sources: append([]string(nil), key.Sources...), Expires: key.Expires, ETag: hex.EncodeToString(digest[:])}
}

func (s *Store) listAccessKeys() []AccessKeyView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]AccessKeyView, 0, len(s.db.AccessKeys))
	for _, key := range s.db.AccessKeys {
		keys = append(keys, accessKeyView(key))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	return keys
}

// Update the existing key without disclosing or replacing its bearer. The
// caller must provide the current fingerprint to avoid overwriting concurrent
// administrative changes. Expired credentials must be reissued, not revived.
func (s *Store) updateAccessKey(id string, sourceIDs []string, expiry time.Time, expected string) (AccessKeyView, error) {
	now := time.Now()
	if !identifier.MatchString(id) || len(sourceIDs) == 0 || len(sourceIDs) > 100 || !expiry.After(now) || expiry.After(now.Add(366*24*time.Hour)) || len(expected) != 64 {
		return AccessKeyView{}, ErrInvalid
	}
	if _, err := hexKey(expected); err != nil {
		return AccessKeyView{}, ErrInvalid
	}
	sources := append([]string(nil), sourceIDs...)
	sort.Strings(sources)
	s.mu.Lock()
	defer s.mu.Unlock()
	now = time.Now()
	if !expiry.After(now) || expiry.After(now.Add(366*24*time.Hour)) {
		return AccessKeyView{}, ErrInvalid
	}
	key, ok := s.db.AccessKeys[id]
	if !ok {
		return AccessKeyView{}, ErrNotFound
	}
	current := accessKeyView(key)
	previousExpiry, err := time.Parse(time.RFC3339Nano, key.Expires)
	if err != nil || !previousExpiry.After(now) || subtle.ConstantTimeCompare([]byte(current.ETag), []byte(expected)) != 1 {
		return AccessKeyView{}, ErrConflict
	}
	for i, source := range sources {
		if _, ok := s.db.Sources[source]; !ok || (i > 0 && sources[i-1] == source) {
			return AccessKeyView{}, ErrInvalid
		}
	}
	key.Sources, key.Expires = sources, expiry.UTC().Format(time.RFC3339Nano)
	db := s.clone()
	db.AccessKeys[id] = key
	if err = s.commit(db); err != nil {
		return AccessKeyView{}, err
	}
	return accessKeyView(key), nil
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
	return s.scopedViewLocked(scope)
}

func (s *Store) scopedViewUser(id string, version int) (*Store, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	scope := s.userScopeLocked(id, version)
	if scope == nil {
		return nil, false, nil
	}
	return s.scopedViewLocked(scope)
}

// Caller holds the store read lock, keeping authorization and snapshot atomic.
func (s *Store) scopedViewLocked(scope []string) (*Store, bool, error) {
	selected := make(map[string]bool, len(scope))
	db := Database{Version: s.db.Version, Sources: map[string]Source{}, Articles: map[string]Article{}, Connections: map[string]GitHubConnection{}, Outbox: map[string]SyncJob{}}
	observed := map[string]time.Time{}
	withheld := map[string]int{}
	for _, id := range scope {
		selected[id] = true
		if source, ok := s.db.Sources[id]; ok {
			if source.Metadata != nil {
				metadata := *source.Metadata
				source.Metadata = &metadata
			}
			source.Assets = maps.Clone(source.Assets)
			db.Sources[id] = source
			if conn, ok := s.db.Connections[source.ConnectionID]; ok {
				db.Connections[conn.ID] = conn
			}
		}
		if t, ok := s.observed[id]; ok {
			observed[id] = t
		}
		if count, ok := s.withheld[id]; ok {
			withheld[id] = count
		}
	}
	for key, article := range s.db.Articles {
		if !selected[article.SourceID] {
			continue
		}
		// Strings are immutable values; copy every mutable field while the
		// source lock still protects the coherent authorization snapshot.
		article.Versions = slices.Clone(article.Versions)
		article.Conflicts = slices.Clone(article.Conflicts)
		if article.Translation != nil {
			translation := *article.Translation
			article.Translation = &translation
		}
		if article.CopiedFrom != nil {
			origin := *article.CopiedFrom
			article.CopiedFrom = &origin
		}
		if article.Published != nil {
			published := *article.Published
			article.Published = &published
		}
		db.Articles[key] = article
	}
	// The reader context exposes only a source-level count, never job IDs,
	// paths, revisions or content. Capture the count inputs under the same lock.
	for id, job := range s.db.Outbox {
		if selected[job.SourceID] && job.State != "complete" && job.State != "superseded" {
			db.Outbox[id] = SyncJob{SourceID: job.SourceID, State: job.State}
		}
	}
	return &Store{dir: s.dir, db: db, observed: observed, withheld: withheld}, true, nil
}
