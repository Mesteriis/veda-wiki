package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

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
	s.RLock()
	defer s.RUnlock()
	keys := make([]AccessKeyView, 0, len(s.DB().AccessKeys))
	for _, key := range s.DB().AccessKeys {
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
	s.Lock()
	defer s.Unlock()
	now = time.Now()
	if !expiry.After(now) || expiry.After(now.Add(366*24*time.Hour)) {
		return AccessKeyView{}, ErrInvalid
	}
	key, ok := s.DB().AccessKeys[id]
	if !ok {
		return AccessKeyView{}, ErrNotFound
	}
	current := accessKeyView(key)
	previousExpiry, err := time.Parse(time.RFC3339Nano, key.Expires)
	if err != nil || !previousExpiry.After(now) || subtle.ConstantTimeCompare([]byte(current.ETag), []byte(expected)) != 1 {
		return AccessKeyView{}, ErrConflict
	}
	for i, source := range sources {
		if _, ok := s.DB().Sources[source]; !ok || (i > 0 && sources[i-1] == source) {
			return AccessKeyView{}, ErrInvalid
		}
	}
	key.Sources, key.Expires = sources, expiry.UTC().Format(time.RFC3339Nano)
	db := s.Clone()
	db.AccessKeys[id] = key
	if err = s.Commit(db); err != nil {
		return AccessKeyView{}, err
	}
	return accessKeyView(key), nil
}

func (s *Store) createAccessKey(id string, sourceIDs []string, expiry time.Time) (string, error) {
	if !identifier.MatchString(id) || len(sourceIDs) == 0 || len(sourceIDs) > 100 || !expiry.After(time.Now()) || expiry.After(time.Now().Add(366*24*time.Hour)) {
		return "", ErrInvalid
	}
	sources := append([]string(nil), sourceIDs...)
	sort.Strings(sources)
	s.Lock()
	defer s.Unlock()
	if _, ok := s.DB().AccessKeys[id]; ok {
		return "", ErrConflict
	}
	for i, source := range sources {
		if _, ok := s.DB().Sources[source]; !ok || (i > 0 && sources[i-1] == source) {
			return "", ErrInvalid
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	token := hex.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	db := s.Clone()
	if db.AccessKeys == nil {
		db.AccessKeys = map[string]AccessKey{}
	}
	db.AccessKeys[id] = AccessKey{ID: id, Hash: hex.EncodeToString(digest[:]), Sources: sources, Expires: expiry.UTC().Format(time.RFC3339Nano)}
	if db.Version < 4 {
		db.Version = 4
	}
	if err := s.Commit(db); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) revokeAccessKey(id string) error {
	s.Lock()
	defer s.Unlock()
	if _, ok := s.DB().AccessKeys[id]; !ok {
		return ErrNotFound
	}
	db := s.Clone()
	delete(db.AccessKeys, id)
	return s.Commit(db)
}

func (s *Store) accessScope(token string, now time.Time) []string {
	s.RLock()
	defer s.RUnlock()
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
	for _, key := range s.DB().AccessKeys {
		hash, err := hex.DecodeString(key.Hash)
		expires, expiryErr := time.Parse(time.RFC3339Nano, key.Expires)
		if err == nil && expiryErr == nil && expires.After(now) && subtle.ConstantTimeCompare(sum[:], hash) == 1 {
			return append([]string(nil), key.Sources...)
		}
	}
	return nil
}

func (s *Store) validScopeHash(hash [32]byte, now time.Time) bool {
	s.RLock()
	defer s.RUnlock()
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
	s.RLock()
	defer s.RUnlock()
	scope := s.accessScopeHashLocked(hash, now)
	if scope == nil {
		return nil, false, nil
	}
	return s.scopedViewLocked(scope)
}

// Caller holds the store read lock, keeping authorization and snapshot atomic.
func (s *Store) scopedViewLocked(scope []string) (*Store, bool, error) {
	return &Store{s.ScopedLocked(scope)}, true, nil
}

func (s *Store) scopedViewUser(id string, version int) (*Store, bool, error) {
	s.RLock()
	defer s.RUnlock()
	scope := s.userScopeLocked(id, version)
	if scope == nil {
		return nil, false, nil
	}
	return s.scopedViewLocked(scope)
}
