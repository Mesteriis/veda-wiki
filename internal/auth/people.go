package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// UserView is the listed form of a named user without password material.
type UserView struct {
	ID       string   `json:"id"`
	Admin    bool     `json:"admin"`
	Teams    []string `json:"teams"`
	Disabled bool     `json:"disabled"`
	Revision int      `json:"revision"`
}

func orderedIDs(values []string, max int) ([]string, error) {
	if len(values) > max {
		return nil, domain.ErrInvalid
	}
	out := append([]string{}, values...)
	sort.Strings(out)
	for i, v := range out {
		if !domain.ValidID(v) || (i > 0 && v == out[i-1]) {
			return nil, domain.ErrInvalid
		}
	}
	return out, nil
}

// SetUserAdmin is called only by an owner-authorized provisioning path.
// Changing the role invalidates sessions issued under the previous role.
func (a Service) SetUserAdmin(id string, admin bool, expected int) error {
	err := a.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		u, ok := db.Users[id]
		if !ok || u.Revision != expected || u.Revision >= 1000000000 || u.AuthVersion >= 1000000000 {
			return domain.ErrConflict
		}
		if u.Admin == admin {
			return errUnchanged
		}
		u.Admin = admin
		u.Revision++
		u.AuthVersion++
		db.Users[id] = u
		return nil
	})
	if errors.Is(err, errUnchanged) {
		return nil
	}
	return err
}

// errUnchanged aborts an Update that would not change anything.
var errUnchanged = errors.New("unchanged")

func otherActiveAdmin(db *domain.Database, id string) bool {
	for _, u := range db.Users {
		if u.ID != id && u.Admin && !u.Disabled && u.SetupExpires == "" {
			return true
		}
	}
	return false
}

// AdminSessionValid reports whether a session issued to id at version still
// belongs to an active administrator.
func (a Service) AdminSessionValid(id string, version int) bool {
	valid := false
	_ = a.Store.View(func(db *domain.Database) error {
		u, ok := db.Users[id]
		valid = ok && !u.Disabled && u.Admin && u.SetupExpires == "" && u.AuthVersion == version
		return nil
	})
	return valid
}

// CreateSuperuser creates an administrator or completes the same pending
// bootstrap account. Existing active accounts are never overwritten.
func (a Service) CreateSuperuser(id, password string) error {
	if !domain.ValidID(id) {
		return domain.ErrInvalid
	}
	salt, hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	return a.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		previous, exists := db.Users[id]
		if exists && (!previous.Admin || previous.Disabled || previous.SetupExpires == "" || previous.Revision >= 1000000000 || previous.AuthVersion >= 1000000000) {
			return domain.ErrConflict
		}
		u := domain.WikiUser{ID: id, Admin: true, Salt: salt, PasswordHash: hash, Teams: []string{}, Revision: 1, AuthVersion: 1}
		if exists {
			u.Revision = previous.Revision + 1
			u.AuthVersion = previous.AuthVersion + 1
		}
		db.Users[id] = u
		return nil
	})
}

// TeamViews returns every team ordered by ID.
func (a Service) TeamViews() []domain.WikiTeam {
	var out []domain.WikiTeam
	_ = a.Store.View(func(db *domain.Database) error {
		out = make([]domain.WikiTeam, 0, len(db.Teams))
		for _, team := range db.Teams {
			team.Sources = append([]string(nil), team.Sources...)
			out = append(out, team)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// UserViews returns every user ordered by ID, without password material.
func (a Service) UserViews() []UserView {
	var out []UserView
	_ = a.Store.View(func(db *domain.Database) error {
		out = make([]UserView, 0, len(db.Users))
		for _, user := range db.Users {
			out = append(out, UserView{ID: user.ID, Admin: user.Admin, Teams: append([]string{}, user.Teams...), Disabled: user.Disabled, Revision: user.Revision})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// SaveTeam creates a team (expected nil) or replaces its sources at the
// expected revision.
func (a Service) SaveTeam(id string, sourceIDs []string, expected *int) (domain.WikiTeam, error) {
	sources, err := orderedIDs(sourceIDs, 100)
	if err != nil || !domain.ValidID(id) || len(sources) == 0 {
		return domain.WikiTeam{}, domain.ErrInvalid
	}
	var team domain.WikiTeam
	err = a.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		current, exists := db.Teams[id]
		if expected == nil {
			if exists {
				return domain.ErrConflict
			}
		} else if !exists || *expected != current.Revision || current.Revision >= 1000000000 {
			return domain.ErrConflict
		}
		for _, source := range sources {
			if _, ok := db.Sources[source]; !ok {
				return domain.ErrInvalid
			}
		}
		team = domain.WikiTeam{ID: id, Sources: sources, Revision: 1}
		if exists {
			team.Revision = current.Revision + 1
		}
		db.Teams[id] = team
		return nil
	})
	if err != nil {
		return domain.WikiTeam{}, err
	}
	return team, nil
}

// SaveUser creates a user (expected nil) or changes teams, password or the
// disabled flag at the expected revision. Password and disabled changes bump
// AuthVersion, which invalidates existing sessions. The last active
// administrator cannot be disabled here.
func (a Service) SaveUser(id string, teams *[]string, password *string, disabled *bool, expected *int) (UserView, error) {
	if !domain.ValidID(id) || teams == nil && password == nil && disabled == nil {
		return UserView{}, domain.ErrInvalid
	}
	var groups []string
	var err error
	if teams != nil {
		groups, err = orderedIDs(*teams, 100)
		if err != nil {
			return UserView{}, err
		}
	}
	var salt, hash string
	if password != nil {
		salt, hash, err = passwordHash(*password)
		if err != nil {
			return UserView{}, err
		}
	}
	var user domain.WikiUser
	err = a.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		var exists bool
		user, exists = db.Users[id]
		if expected == nil {
			if exists {
				return domain.ErrConflict
			}
			if password == nil || teams == nil {
				return domain.ErrInvalid
			}
			user = domain.WikiUser{ID: id, Revision: 1, AuthVersion: 1}
		}
		if expected != nil && (!exists || user.Revision != *expected || user.Revision >= 1000000000 || user.AuthVersion > 999999998) {
			return domain.ErrConflict
		}
		if teams != nil {
			for _, team := range groups {
				if _, ok := db.Teams[team]; !ok {
					return domain.ErrInvalid
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
			if *disabled && user.Admin && !otherActiveAdmin(db, id) {
				return domain.ErrConflict
			}
			user.Disabled = *disabled
			if exists {
				user.AuthVersion++
			}
		}
		if exists {
			user.Revision++
		}
		db.Users[id] = user
		return nil
	})
	if err != nil {
		return UserView{}, err
	}
	return UserView{ID: user.ID, Admin: user.Admin, Teams: append([]string{}, user.Teams...), Disabled: user.Disabled, Revision: user.Revision}, nil
}

// ChangePassword sets a new password for the user of a session issued at
// authVersion, at the user's current revision. It fails with ErrConflict when
// the user is gone or the session's AuthVersion is no longer current.
func (a Service) ChangePassword(id string, authVersion int, password string) error {
	revision, exists := 0, false
	_ = a.Store.View(func(db *domain.Database) error {
		current, ok := db.Users[id]
		exists = ok && current.AuthVersion == authVersion
		revision = current.Revision
		return nil
	})
	if !exists {
		return domain.ErrConflict
	}
	_, err := a.SaveUser(id, nil, &password, nil, &revision)
	return err
}

// AuthenticateUser is AuthenticateUserContext without a request context.
func (a Service) AuthenticateUser(id, password string) (int, bool) {
	return a.AuthenticateUserContext(context.Background(), id, password)
}

// AuthenticateUserContext always performs Argon2 for valid-size input,
// including unknown users. The bounded password workers limit simultaneous
// memory use; a request whose ctx ends while it waits for one fails. It
// rechecks after hashing so a concurrent disable or password rotation cannot
// authenticate a stale secret. It returns the user's AuthVersion.
func (a Service) AuthenticateUserContext(ctx context.Context, id, password string) (int, bool) {
	if !PasswordValid(password) {
		return 0, false
	}
	user, exists := a.user(id)
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
	derived, err := derive(ctx, password, salt)
	if err != nil || !exists || user.Disabled || user.SetupExpires != "" || subtle.ConstantTimeCompare(derived, expected) != 1 {
		return 0, false
	}
	current, ok := a.user(id)
	return user.AuthVersion, ok && !current.Disabled && current.AuthVersion == user.AuthVersion && current.PasswordHash == user.PasswordHash
}

// user reads one user record under the read lock.
func (a Service) user(id string) (domain.WikiUser, bool) {
	var user domain.WikiUser
	var ok bool
	_ = a.Store.View(func(db *domain.Database) error {
		user, ok = db.Users[id]
		return nil
	})
	return user, ok
}

// BootstrapAdmin is restricted to the root-authenticated local socket. It
// creates a pending administrator and returns a one-time setup token that
// expires after 24 hours.
func (a Service) BootstrapAdmin(id string) (string, error) {
	if !domain.ValidID(id) {
		return "", domain.ErrInvalid
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	salt, hash, err := passwordHash(token)
	if err != nil {
		return "", err
	}
	err = a.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		previous, exists := db.Users[id]
		if exists && (!previous.Admin || previous.SetupExpires == "" || previous.Disabled || previous.Revision >= 1000000000 || previous.AuthVersion >= 1000000000) {
			return domain.ErrConflict
		}
		for _, user := range db.Users {
			if user.Admin && user.ID != id {
				return domain.ErrConflict
			}
		}
		u := domain.WikiUser{ID: id, Admin: true, Salt: salt, PasswordHash: hash, Teams: []string{}, Revision: 1, AuthVersion: 1, SetupExpires: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)}
		if exists {
			u.Revision = previous.Revision + 1
			u.AuthVersion = previous.AuthVersion + 1
		}
		db.Users[id] = u
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// FinishAdminSetup is FinishAdminSetupContext without a request context.
func (a Service) FinishAdminSetup(id, token, password string) error {
	return a.FinishAdminSetupContext(context.Background(), id, token, password)
}

// FinishAdminSetupContext replaces a pending administrator's setup token with
// a password before the token expires. A request whose ctx ends while it
// waits for a password worker fails with ErrConflict, as a wrong token does.
func (a Service) FinishAdminSetupContext(ctx context.Context, id, token, password string) error {
	if !domain.ValidID(id) || !PasswordValid(token) || !PasswordValid(password) {
		return domain.ErrInvalid
	}
	u, ok := a.user(id)
	expires, err := time.Parse(time.RFC3339Nano, u.SetupExpires)
	if !ok || !u.Admin || u.Disabled || err != nil || !expires.After(time.Now()) {
		return domain.ErrConflict
	}
	salt, err := hex.DecodeString(u.Salt)
	if err != nil {
		return domain.ErrConflict
	}
	expected, err := hex.DecodeString(u.PasswordHash)
	if err != nil {
		return domain.ErrConflict
	}
	derived, err := derive(ctx, token, salt)
	if err != nil || subtle.ConstantTimeCompare(derived, expected) != 1 {
		return domain.ErrConflict
	}
	newSalt, newHash, err := passwordHash(password)
	if err != nil {
		return err
	}
	return a.Store.Update(func(tx *store.Tx) error {
		db := tx.DB()
		current, ok := db.Users[id]
		if !ok || current.Revision != u.Revision || current.PasswordHash != u.PasswordHash || current.SetupExpires != u.SetupExpires || !expires.After(time.Now()) {
			return domain.ErrConflict
		}
		current.Salt, current.PasswordHash, current.SetupExpires = newSalt, newHash, ""
		current.Revision++
		current.AuthVersion++
		db.Users[id] = current
		return nil
	})
}

// userScope returns the sources a named user's session at authVersion may
// read: every source for an administrator, plus the sources of the user's
// teams. It returns nil for an invalid session.
func userScope(db *domain.Database, id string, authVersion int) []string {
	user, ok := db.Users[id]
	if !ok || user.Disabled || user.SetupExpires != "" || user.AuthVersion != authVersion {
		return nil
	}
	seen := map[string]bool{}
	if user.Admin {
		for id := range db.Sources {
			seen[id] = true
		}
	}
	for _, id := range user.Teams {
		for _, source := range db.Teams[id].Sources {
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

// UserScope is userScope under the read lock.
func (a Service) UserScope(id string, authVersion int) []string {
	var scope []string
	_ = a.Store.View(func(db *domain.Database) error {
		scope = userScope(db, id, authVersion)
		return nil
	})
	return scope
}

// UserSessionValid reports whether a named user's session is still valid.
func (a Service) UserSessionValid(id string, version int) bool {
	return a.UserScope(id, version) != nil
}
