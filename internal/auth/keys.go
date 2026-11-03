package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// AccessKeyView is the listed form of an access key; it never contains the
// bearer or its digest.
type AccessKeyView struct {
	ID      string   `json:"id"`
	Sources []string `json:"sources"`
	Expires string   `json:"expires_at"`
	ETag    string   `json:"etag,omitempty"`
}

func accessKeyView(key domain.AccessKey) AccessKeyView {
	// The bearer is random; only its stored digest enters this opaque fingerprint.
	// Including it distinguishes a revoked/reissued key with the same metadata.
	encoded, _ := json.Marshal(key)
	digest := sha256.Sum256(encoded)
	return AccessKeyView{ID: key.ID, Sources: append([]string(nil), key.Sources...), Expires: key.Expires, ETag: hex.EncodeToString(digest[:])}
}

// ListAccessKeys returns every key's metadata ordered by ID.
func (a Service) ListAccessKeys() []AccessKeyView {
	var keys []AccessKeyView
	_ = a.Store.View(func(db *domain.Database) error {
		keys = make([]AccessKeyView, 0, len(db.AccessKeys))
		for _, key := range db.AccessKeys {
			keys = append(keys, accessKeyView(key))
		}
		return nil
	})
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	return keys
}

// UpdateAccessKey changes the existing key without disclosing or replacing its
// bearer. The caller must provide the current fingerprint to avoid overwriting
// concurrent administrative changes. Expired credentials must be reissued, not
// revived.
func (a Service) UpdateAccessKey(id string, sourceIDs []string, expiry time.Time, expected string) (AccessKeyView, error) {
	now := time.Now()
	if !domain.ValidID(id) || len(sourceIDs) == 0 || len(sourceIDs) > 100 || !expiry.After(now) || expiry.After(now.Add(366*24*time.Hour)) || len(expected) != 64 {
		return AccessKeyView{}, domain.ErrInvalid
	}
	if _, err := domain.HexKey(expected); err != nil {
		return AccessKeyView{}, domain.ErrInvalid
	}
	sources := append([]string(nil), sourceIDs...)
	sort.Strings(sources)
	var updated AccessKeyView
	err := a.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		now := time.Now()
		if !expiry.After(now) || expiry.After(now.Add(366*24*time.Hour)) {
			return domain.ErrInvalid
		}
		key, ok := db.AccessKeys[id]
		if !ok {
			return domain.ErrNotFound
		}
		current := accessKeyView(key)
		previousExpiry, err := time.Parse(time.RFC3339Nano, key.Expires)
		if err != nil || !previousExpiry.After(now) || subtle.ConstantTimeCompare([]byte(current.ETag), []byte(expected)) != 1 {
			return domain.ErrConflict
		}
		for i, source := range sources {
			if _, ok := db.Sources[source]; !ok || (i > 0 && sources[i-1] == source) {
				return domain.ErrInvalid
			}
		}
		key.Sources, key.Expires = sources, expiry.UTC().Format(time.RFC3339Nano)
		db.AccessKeys[id] = key
		updated = accessKeyView(key)
		return nil
	})
	if err != nil {
		return AccessKeyView{}, err
	}
	return updated, nil
}

// CreateAccessKey stores a new key for the given sources and returns its
// bearer. Only the bearer's SHA-256 digest is stored.
func (a Service) CreateAccessKey(id string, sourceIDs []string, expiry time.Time) (string, error) {
	if !domain.ValidID(id) || len(sourceIDs) == 0 || len(sourceIDs) > 100 || !expiry.After(time.Now()) || expiry.After(time.Now().Add(366*24*time.Hour)) {
		return "", domain.ErrInvalid
	}
	sources := append([]string(nil), sourceIDs...)
	sort.Strings(sources)
	var token string
	err := a.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		if _, ok := db.AccessKeys[id]; ok {
			return domain.ErrConflict
		}
		for i, source := range sources {
			if _, ok := db.Sources[source]; !ok || (i > 0 && sources[i-1] == source) {
				return domain.ErrInvalid
			}
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		token = hex.EncodeToString(secret)
		digest := sha256.Sum256([]byte(token))
		if db.AccessKeys == nil {
			db.AccessKeys = map[string]domain.AccessKey{}
		}
		db.AccessKeys[id] = domain.AccessKey{ID: id, Hash: hex.EncodeToString(digest[:]), Sources: sources, Expires: expiry.UTC().Format(time.RFC3339Nano)}
		if db.Version < 4 {
			db.Version = 4
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// RevokeAccessKey deletes a key.
func (a Service) RevokeAccessKey(id string) error {
	return a.Store.Update(func(tx *store.Tx) error {
		if _, ok := tx.DB().AccessKeys[id]; !ok {
			return domain.ErrNotFound
		}
		delete(tx.DB().AccessKeys, id)
		return nil
	})
}

// AccessScope returns the sources a valid, unexpired bearer may read, or nil.
func (a Service) AccessScope(token string, now time.Time) []string {
	sum, ok := tokenHash(token)
	if !ok {
		return nil
	}
	var scope []string
	_ = a.Store.View(func(db *domain.Database) error {
		scope = accessScopeHash(db, sum, now)
		return nil
	})
	return scope
}

// tokenHash returns the digest of a well-formed bearer.
func tokenHash(token string) ([32]byte, bool) {
	if len(token) != 64 {
		return [32]byte{}, false
	}
	if _, err := hex.DecodeString(token); err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(token)), true
}

// accessScopeHash compares sum with every stored digest in constant time.
func accessScopeHash(db *domain.Database, sum [32]byte, now time.Time) []string {
	for _, key := range db.AccessKeys {
		hash, err := hex.DecodeString(key.Hash)
		expires, expiryErr := time.Parse(time.RFC3339Nano, key.Expires)
		if err == nil && expiryErr == nil && expires.After(now) && subtle.ConstantTimeCompare(sum[:], hash) == 1 {
			return append([]string(nil), key.Sources...)
		}
	}
	return nil
}

// ValidScopeHash reports whether a bearer digest still grants access.
func (a Service) ValidScopeHash(hash [32]byte, now time.Time) bool {
	valid := false
	_ = a.Store.View(func(db *domain.Database) error {
		valid = accessScopeHash(db, hash, now) != nil
		return nil
	})
	return valid
}

// ScopedView builds an immutable request-local snapshot under the same lock
// as key verification. Every reader surface then operates only on that
// snapshot; no caller-provided source selector can widen it.
func (a Service) ScopedView(token string, now time.Time) (*store.Store, bool, error) {
	sum, ok := tokenHash(token)
	if !ok {
		return nil, false, nil
	}
	return a.ScopedViewHash(sum, now)
}

// ScopedViewHash is ScopedView for a bearer digest held by a browser session.
func (a Service) ScopedViewHash(hash [32]byte, now time.Time) (*store.Store, bool, error) {
	return a.Store.ScopedWhere(func(db *domain.Database) []string {
		return accessScopeHash(db, hash, now)
	})
}

// ScopedViewUser is ScopedView for a named user's session.
func (a Service) ScopedViewUser(id string, authVersion int) (*store.Store, bool, error) {
	return a.Store.ScopedWhere(func(db *domain.Database) []string {
		return userScope(db, id, authVersion)
	})
}
