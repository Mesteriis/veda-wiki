package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Browser session cookie names.
const (
	ReaderCookie = "veda_reader_session"
	AdminCookie  = "veda_admin_session"
	AccessCookie = "veda_access_session"
)

const (
	sessionLifetime = 8 * time.Hour
	sessionLimit    = 100
)

// Login failures reported by Sessions.Issue.
var (
	ErrRateLimited  = errors.New("try again later")
	ErrDenied       = errors.New("access denied")
	ErrSessionLimit = errors.New("session limit")
)

// Sessions is one in-memory table of browser sessions, keyed by the SHA-256
// of the cookie value. A session is full (reader or owner), scoped to an
// access key, or bound to a named user.
type Sessions struct {
	mu       sync.Mutex
	active   map[[32]byte]time.Time
	scoped   map[[32]byte][32]byte
	users    map[[32]byte]SessionUser
	attempts map[[32]byte][]time.Time
}

// SessionUser identifies the named user of a session and the AuthVersion it
// was issued under.
type SessionUser struct {
	ID          string
	AuthVersion int
}

// Grant is what a successful login authorizes: a full session (zero value),
// a session scoped to an access key digest, or a named user's session.
type Grant struct {
	Scoped bool
	Scope  [32]byte
	Named  bool
	User   SessionUser
}

// Login describes one login attempt for Sessions.Issue.
type Login struct {
	Now time.Time
	// Limited counts the attempt against Kind and Identity first. Random
	// 256-bit key logins are not limited.
	Limited        bool
	Kind, Identity string
	// Authenticate checks the credentials and returns what they grant.
	Authenticate func() (Grant, bool)
	// Current reports whether an existing scoped or named session is still
	// valid; invalid ones are pruned before the session limit is applied. A
	// nil Current treats every scoped and named session as invalid.
	Current func(Grant) bool
}

// SecureBrowserRequest reports whether a request may carry session cookies:
// it arrived over TLS, or over loopback for a loopback or localhost host.
func SecureBrowserRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(remote) == nil || !net.ParseIP(remote).IsLoopback() {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

// SameOrigin reports whether the Origin header names exactly this host and
// scheme.
func SameOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host != r.Host || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.User != nil {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return origin.Scheme == scheme
}

// Valid reports whether the reader cookie holds a live full session.
func (s *Sessions) Valid(r *http.Request) bool {
	return s.ValidCookie(r, ReaderCookie)
}

// ValidCookie reports whether the named cookie holds a live full session.
func (s *Sessions) ValidCookie(r *http.Request, name string) bool {
	if s == nil || !SecureBrowserRequest(r) {
		return false
	}
	c, err := r.Cookie(name)
	if err != nil || len(c.Value) != 64 {
		return false
	}
	hash := sha256.Sum256([]byte(c.Value))
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.active[hash]
	if !ok || !expiry.After(time.Now()) {
		s.forget(hash)
		return false
	}
	_, scoped := s.scoped[hash]
	_, user := s.users[hash]
	return !scoped && !user
}

// UserCookie returns the named user of the reader cookie's session.
func (s *Sessions) UserCookie(r *http.Request) (SessionUser, bool) {
	return s.NamedUserCookie(r, ReaderCookie)
}

// NamedUserCookie returns the named user of the given cookie's session.
func (s *Sessions) NamedUserCookie(r *http.Request, name string) (SessionUser, bool) {
	if s == nil || !SecureBrowserRequest(r) {
		return SessionUser{}, false
	}
	c, err := r.Cookie(name)
	if err != nil || len(c.Value) != 64 {
		return SessionUser{}, false
	}
	hash := sha256.Sum256([]byte(c.Value))
	s.mu.Lock()
	defer s.mu.Unlock()
	if expiry, ok := s.active[hash]; !ok || !expiry.After(time.Now()) {
		s.forget(hash)
		return SessionUser{}, false
	}
	user, ok := s.users[hash]
	return user, ok
}

// ScopedCookie returns the access key digest of the reader cookie's session.
func (s *Sessions) ScopedCookie(r *http.Request) ([32]byte, bool) {
	if s == nil || !SecureBrowserRequest(r) {
		return [32]byte{}, false
	}
	c, err := r.Cookie(ReaderCookie)
	if err != nil || len(c.Value) != 64 {
		return [32]byte{}, false
	}
	hash := sha256.Sum256([]byte(c.Value))
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.active[hash]
	if !ok || !expiry.After(time.Now()) {
		s.forget(hash)
		return [32]byte{}, false
	}
	scope, ok := s.scoped[hash]
	return scope, ok
}

// Revoke ends the session whose cookie holds value.
func (s *Sessions) Revoke(value string) {
	hash := sha256.Sum256([]byte(value))
	s.mu.Lock()
	s.forget(hash)
	s.mu.Unlock()
}

// Caller holds the session mutex.
func (s *Sessions) forget(hash [32]byte) {
	delete(s.active, hash)
	delete(s.scoped, hash)
	delete(s.users, hash)
}

// Issue runs one login attempt: the per-identity limit under the session
// lock, then the credential check without it, so slow password checks run
// concurrently up to the password workers and do not delay cookie checks,
// then, under the lock again, pruning of expired or no longer valid sessions,
// the session limit and issuance. The attempt is counted before the check.
// It returns the new cookie value, valid for eight hours from l.Now.
func (s *Sessions) Issue(l Login) (string, error) {
	if l.Limited && !s.AllowAttempt(l.Kind, l.Identity, l.Now) {
		return "", ErrRateLimited
	}
	grant, ok := l.Authenticate()
	if !ok {
		return "", ErrDenied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := l.Current
	if current == nil {
		current = func(Grant) bool { return false }
	}
	for h, expiry := range s.active {
		scope, restricted := s.scoped[h]
		user, isUser := s.users[h]
		if !expiry.After(l.Now) || (restricted && !current(Grant{Scoped: true, Scope: scope})) || (isUser && !current(Grant{Named: true, User: user})) {
			s.forget(h)
		}
	}
	if len(s.active) >= sessionLimit {
		return "", ErrSessionLimit
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	value := hex.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(value))
	if s.active == nil {
		s.active = map[[32]byte]time.Time{}
	}
	s.active[hash] = l.Now.Add(sessionLifetime)
	if grant.Scoped {
		if s.scoped == nil {
			s.scoped = map[[32]byte][32]byte{}
		}
		s.scoped[hash] = grant.Scope
	}
	if grant.Named {
		if s.users == nil {
			s.users = map[[32]byte]SessionUser{}
		}
		s.users[hash] = grant.User
	}
	return value, nil
}

// Count returns the number of live-or-unpruned sessions and how many of them
// are scoped to an access key.
//
// Test seam: used by tests in this package and by handler tests in httpapi;
// production code does not call it.
func (s *Sessions) Count() (active, scoped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active), len(s.scoped)
}

// SetCookie sets a session cookie: HttpOnly, SameSite=Strict, Secure on TLS,
// for the session lifetime.
func SetCookie(rw http.ResponseWriter, r *http.Request, name, value string) {
	http.SetCookie(rw, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: 8 * 60 * 60, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
}

// ClearCookie expires a session cookie with the same attributes.
func ClearCookie(rw http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(rw, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
}
