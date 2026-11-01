package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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
	db.Version = 4
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
	if len(token) != 64 {
		return nil
	}
	if _, err := hex.DecodeString(token); err != nil {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range s.db.AccessKeys {
		hash, err := hex.DecodeString(key.Hash)
		expires, expiryErr := time.Parse(time.RFC3339Nano, key.Expires)
		if err == nil && expiryErr == nil && expires.After(now) && subtle.ConstantTimeCompare(sum[:], hash) == 1 {
			return append([]string(nil), key.Sources...)
		}
	}
	return nil
}
