package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sort"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Human identities are distinct from API/agent keys. Teams grant read-only
// access to named sources. Password hashes never appear in administrative views.
type WikiTeam struct {
	ID       string   `json:"id"`
	Sources  []string `json:"sources"`
	Revision int      `json:"revision"`
}
type WikiUser struct {
	ID           string   `json:"id"`
	Admin        bool     `json:"admin,omitempty"`
	SetupExpires string   `json:"setup_expires,omitempty"`
	Salt         string   `json:"salt"`
	PasswordHash string   `json:"password_hash"`
	Teams        []string `json:"teams"`
	Disabled     bool     `json:"disabled"`
	Revision     int      `json:"revision"`
	AuthVersion  int      `json:"auth_version"`
}
type WikiUserView struct {
	ID       string   `json:"id"`
	Admin    bool     `json:"admin"`
	Teams    []string `json:"teams"`
	Disabled bool     `json:"disabled"`
	Revision int      `json:"revision"`
}

const passwordMemoryKiB = 19 * 1024

func passwordValid(p string) bool {
	n := utf8.RuneCountInString(p)
	return utf8.ValidString(p) && len(p) <= 512 && n >= 10 && n <= 128
}
func orderedIDs(values []string, max int) ([]string, error) {
	if len(values) > max {
		return nil, ErrInvalid
	}
	out := append([]string{}, values...)
	sort.Strings(out)
	for i, v := range out {
		if !identifier.MatchString(v) || (i > 0 && v == out[i-1]) {
			return nil, ErrInvalid
		}
	}
	return out, nil
}
func validatePeople(db Database) error {
	if (len(db.Users) > 0 || len(db.Teams) > 0) && db.Version < 15 {
		return errors.New("users and teams require store version 15")
	}
	for id, team := range db.Teams {
		if id != team.ID || !identifier.MatchString(id) || team.Revision < 1 || team.Revision > 1000000000 || len(team.Sources) == 0 || len(team.Sources) > 100 {
			return errors.New("invalid team")
		}
		for i, source := range team.Sources {
			if _, ok := db.Sources[source]; !ok || (i > 0 && team.Sources[i-1] >= source) {
				return errors.New("invalid team scope")
			}
		}
	}
	for id, user := range db.Users {
		if user.Admin && db.Version < 20 {
			return errors.New("administrators require store version 20")
		}
		if user.SetupExpires != "" {
			if _, err := time.Parse(time.RFC3339Nano, user.SetupExpires); err != nil || !user.Admin {
				return errors.New("invalid administrator setup")
			}
		}
		if id != user.ID || !identifier.MatchString(id) || user.Revision < 1 || user.Revision > 1000000000 || user.AuthVersion < 1 || user.AuthVersion > 1000000000 || len(user.Teams) > 100 {
			return errors.New("invalid user")
		}
		salt, e := hex.DecodeString(user.Salt)
		if e != nil || len(salt) != 16 {
			return errors.New("invalid user salt")
		}
		hash, e := hex.DecodeString(user.PasswordHash)
		if e != nil || len(hash) != 32 {
			return errors.New("invalid user password hash")
		}
		for i, team := range user.Teams {
			if _, ok := db.Teams[team]; !ok || (i > 0 && user.Teams[i-1] >= team) {
				return errors.New("invalid user teams")
			}
		}
	}
	return nil
}

// setUserAdmin is called only by an owner-authorized provisioning path.
// Changing the role invalidates sessions issued under the previous role.
func (s *Store) setUserAdmin(id string, admin bool, expected int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.db.Users[id]
	if !ok || u.Revision != expected || u.Revision >= 1000000000 || u.AuthVersion >= 1000000000 {
		return ErrConflict
	}
	if u.Admin == admin {
		return nil
	}
	u.Admin = admin
	u.Revision++
	u.AuthVersion++
	db := s.clone()
	db.Users[id] = u
	if db.Version < 20 {
		db.Version = 20
	}
	return s.commit(db)
}

func (s *Store) adminSessionValid(id string, version int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.db.Users[id]
	return ok && !u.Disabled && u.Admin && u.SetupExpires == "" && u.AuthVersion == version
}

// createSuperuser creates an administrator or completes the same pending
// bootstrap account. Existing active accounts are never overwritten.
func (s *Store) createSuperuser(id, password string) error {
	if !identifier.MatchString(id) {
		return ErrInvalid
	}
	salt, hash, err := s.passwordHash(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.db.Users[id]
	if exists && (!previous.Admin || previous.Disabled || previous.SetupExpires == "" || previous.Revision >= 1000000000 || previous.AuthVersion >= 1000000000) {
		return ErrConflict
	}
	u := WikiUser{ID: id, Admin: true, Salt: salt, PasswordHash: hash, Teams: []string{}, Revision: 1, AuthVersion: 1}
	if exists {
		u.Revision = previous.Revision + 1
		u.AuthVersion = previous.AuthVersion + 1
	}
	db := s.clone()
	db.Users[id] = u
	if db.Version < 20 {
		db.Version = 20
	}
	return s.commit(db)
}
func (s *Store) passwordHash(password string) (string, string, error) {
	if !passwordValid(password) {
		return "", "", ErrInvalid
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", "", err
	}
	s.passwordMu.Lock()
	derived := argon2.IDKey([]byte(password), salt, 2, passwordMemoryKiB, 1, 32)
	s.passwordMu.Unlock()
	return hex.EncodeToString(salt), hex.EncodeToString(derived), nil
}
func (s *Store) teamViews() []WikiTeam {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]WikiTeam, 0, len(s.db.Teams))
	for _, team := range s.db.Teams {
		team.Sources = append([]string(nil), team.Sources...)
		out = append(out, team)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) userViews() []WikiUserView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]WikiUserView, 0, len(s.db.Users))
	for _, user := range s.db.Users {
		out = append(out, WikiUserView{ID: user.ID, Admin: user.Admin, Teams: append([]string{}, user.Teams...), Disabled: user.Disabled, Revision: user.Revision})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) saveTeam(id string, sourceIDs []string, expected *int) (WikiTeam, error) {
	sources, err := orderedIDs(sourceIDs, 100)
	if err != nil || !identifier.MatchString(id) || len(sources) == 0 {
		return WikiTeam{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.db.Teams[id]
	if expected == nil {
		if exists {
			return WikiTeam{}, ErrConflict
		}
	} else if !exists || *expected != current.Revision || current.Revision >= 1000000000 {
		return WikiTeam{}, ErrConflict
	}
	for _, source := range sources {
		if _, ok := s.db.Sources[source]; !ok {
			return WikiTeam{}, ErrInvalid
		}
	}
	team := WikiTeam{ID: id, Sources: sources, Revision: 1}
	if exists {
		team.Revision = current.Revision + 1
	}
	db := s.clone()
	db.Teams[id] = team
	if db.Version < 15 {
		db.Version = 15
	}
	if err = s.commit(db); err != nil {
		return WikiTeam{}, err
	}
	return team, nil
}
func (s *Store) saveUser(id string, teams *[]string, password *string, disabled *bool, expected *int) (WikiUserView, error) {
	if !identifier.MatchString(id) || teams == nil && password == nil && disabled == nil {
		return WikiUserView{}, ErrInvalid
	}
	var groups []string
	var err error
	if teams != nil {
		groups, err = orderedIDs(*teams, 100)
		if err != nil {
			return WikiUserView{}, err
		}
	}
	var salt, hash string
	if password != nil {
		salt, hash, err = s.passwordHash(*password)
		if err != nil {
			return WikiUserView{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, exists := s.db.Users[id]
	if expected == nil {
		if exists {
			return WikiUserView{}, ErrConflict
		}
		if password == nil || teams == nil {
			return WikiUserView{}, ErrInvalid
		}
		user = WikiUser{ID: id, Revision: 1, AuthVersion: 1}
	}
	if expected != nil && (!exists || user.Revision != *expected || user.Revision >= 1000000000 || user.AuthVersion > 999999998) {
		return WikiUserView{}, ErrConflict
	}
	if teams != nil {
		for _, team := range groups {
			if _, ok := s.db.Teams[team]; !ok {
				return WikiUserView{}, ErrInvalid
			}
		}
		user.Teams = groups
	}
	if password != nil {
		user.Salt, user.PasswordHash = salt, hash
		user.SetupExpires = ""
		if exists {
			user.AuthVersion++
		}
	}
	if disabled != nil && user.Disabled != *disabled {
		user.Disabled = *disabled
		if exists {
			user.AuthVersion++
		}
	}
	if exists {
		user.Revision++
	}
	db := s.clone()
	db.Users[id] = user
	if db.Version < 15 {
		db.Version = 15
	}
	if err = s.commit(db); err != nil {
		return WikiUserView{}, err
	}
	return WikiUserView{ID: user.ID, Admin: user.Admin, Teams: append([]string{}, user.Teams...), Disabled: user.Disabled, Revision: user.Revision}, nil
}

// Always perform Argon2 for valid-size input, including unknown users. The
// password mutex bounds simultaneous memory use. Recheck after hashing so a
// concurrent disable or password rotation cannot authenticate a stale secret.
func (s *Store) authenticateUser(id, password string) (int, bool) {
	if !passwordValid(password) {
		return 0, false
	}
	s.mu.RLock()
	user, exists := s.db.Users[id]
	s.mu.RUnlock()
	salt := make([]byte, 16)
	expected := make([]byte, 32)
	if exists {
		if b, e := hex.DecodeString(user.Salt); e == nil && len(b) == 16 {
			copy(salt, b)
		}
		if b, e := hex.DecodeString(user.PasswordHash); e == nil && len(b) == 32 {
			copy(expected, b)
		}
	}
	s.passwordMu.Lock()
	derived := argon2.IDKey([]byte(password), salt, 2, passwordMemoryKiB, 1, 32)
	s.passwordMu.Unlock()
	if !exists || user.Disabled || user.SetupExpires != "" || subtle.ConstantTimeCompare(derived, expected) != 1 {
		return 0, false
	}
	s.mu.RLock()
	current, ok := s.db.Users[id]
	s.mu.RUnlock()
	return user.AuthVersion, ok && !current.Disabled && current.AuthVersion == user.AuthVersion && current.PasswordHash == user.PasswordHash
}

// bootstrapAdmin is restricted to the root-authenticated local socket.
func (s *Store) bootstrapAdmin(id string) (string, error) {
	if !identifier.MatchString(id) {
		return "", ErrInvalid
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	salt, hash, err := s.passwordHash(token)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.db.Users[id]
	if exists && (!previous.Admin || previous.SetupExpires == "" || previous.Disabled || previous.Revision >= 1000000000 || previous.AuthVersion >= 1000000000) {
		return "", ErrConflict
	}
	for _, user := range s.db.Users {
		if user.Admin && user.ID != id {
			return "", ErrConflict
		}
	}
	db := s.clone()
	u := WikiUser{ID: id, Admin: true, Salt: salt, PasswordHash: hash, Teams: []string{}, Revision: 1, AuthVersion: 1, SetupExpires: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)}
	if exists {
		u.Revision = previous.Revision + 1
		u.AuthVersion = previous.AuthVersion + 1
	}
	db.Users[id] = u
	if db.Version < 20 {
		db.Version = 20
	}
	if err = s.commit(db); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) finishAdminSetup(id, token, password string) error {
	if !identifier.MatchString(id) || !passwordValid(token) || !passwordValid(password) {
		return ErrInvalid
	}
	s.mu.RLock()
	u, ok := s.db.Users[id]
	s.mu.RUnlock()
	expires, err := time.Parse(time.RFC3339Nano, u.SetupExpires)
	if !ok || !u.Admin || u.Disabled || err != nil || !expires.After(time.Now()) {
		return ErrConflict
	}
	salt, err := hex.DecodeString(u.Salt)
	if err != nil {
		return ErrConflict
	}
	expected, err := hex.DecodeString(u.PasswordHash)
	if err != nil {
		return ErrConflict
	}
	s.passwordMu.Lock()
	derived := argon2.IDKey([]byte(token), salt, 2, passwordMemoryKiB, 1, 32)
	s.passwordMu.Unlock()
	if subtle.ConstantTimeCompare(derived, expected) != 1 {
		return ErrConflict
	}
	newSalt, newHash, err := s.passwordHash(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.db.Users[id]
	if !ok || current.Revision != u.Revision || current.PasswordHash != u.PasswordHash || current.SetupExpires != u.SetupExpires || !expires.After(time.Now()) {
		return ErrConflict
	}
	current.Salt, current.PasswordHash, current.SetupExpires = newSalt, newHash, ""
	current.Revision++
	current.AuthVersion++
	db := s.clone()
	db.Users[id] = current
	return s.commit(db)
}
func (s *Store) userScopeLocked(id string, authVersion int) []string {
	user, ok := s.db.Users[id]
	if !ok || user.Disabled || user.SetupExpires != "" || user.AuthVersion != authVersion {
		return nil
	}
	seen := map[string]bool{}
	if user.Admin {
		for id := range s.db.Sources {
			seen[id] = true
		}
	}
	for _, id := range user.Teams {
		for _, source := range s.db.Teams[id].Sources {
			seen[source] = true
		}
	}
	scope := make([]string, 0, len(seen))
	for source := range seen {
		scope = append(scope, source)
	}
	sort.Strings(scope)
	return scope
}
func (s *Store) userSessionValid(id string, version int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userScopeLocked(id, version) != nil
}
