package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "veda_reader_session"
const adminSessionCookie = "veda_admin_session"
const accessSessionCookie = "veda_access_session"

type readerSessions struct {
	mu       sync.Mutex
	active   map[[32]byte]time.Time
	scoped   map[[32]byte][32]byte
	users    map[[32]byte]userSession
	attempts []time.Time
}

type userSession struct {
	ID          string
	AuthVersion int
}

// Caller holds the session mutex. The same global limiter protects login and
// password confirmation, including attempts against unknown identities.
func (s *readerSessions) allowAttempt(now time.Time) bool {
	kept := s.attempts[:0]
	for _, t := range s.attempts {
		if t.After(now.Add(-time.Minute)) {
			kept = append(kept, t)
		}
	}
	s.attempts = kept
	if len(kept) >= 10 {
		return false
	}
	s.attempts = append(s.attempts, now)
	return true
}

func secureBrowserRequest(r *http.Request) bool {
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
func sameOrigin(r *http.Request) bool {
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
func (s *readerSessions) valid(r *http.Request) bool {
	return s.validCookie(r, sessionCookie)
}
func (s *readerSessions) validCookie(r *http.Request, name string) bool {
	if s == nil || !secureBrowserRequest(r) {
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
		delete(s.active, hash)
		delete(s.scoped, hash)
		delete(s.users, hash)
		return false
	}
	_, scoped := s.scoped[hash]
	_, user := s.users[hash]
	return !scoped && !user
}

func (s *readerSessions) userCookie(r *http.Request) (userSession, bool) {
	return s.namedUserCookie(r, sessionCookie)
}

func (s *readerSessions) namedUserCookie(r *http.Request, name string) (userSession, bool) {
	if s == nil || !secureBrowserRequest(r) {
		return userSession{}, false
	}
	c, err := r.Cookie(name)
	if err != nil || len(c.Value) != 64 {
		return userSession{}, false
	}
	hash := sha256.Sum256([]byte(c.Value))
	s.mu.Lock()
	defer s.mu.Unlock()
	if expiry, ok := s.active[hash]; !ok || !expiry.After(time.Now()) {
		delete(s.active, hash)
		delete(s.scoped, hash)
		delete(s.users, hash)
		return userSession{}, false
	}
	user, ok := s.users[hash]
	return user, ok
}

func (w Wiki) adminCookieValid(r *http.Request, sessions *readerSessions, cookie string) bool {
	if sessions.validCookie(r, cookie) {
		return true
	}
	u, ok := sessions.namedUserCookie(r, cookie)
	if ok && w.Store != nil && w.Store.adminSessionValid(u.ID, u.AuthVersion) {
		return true
	}
	return w.readerAdminCookieValid(r)
}

func (w Wiki) readerAdminCookieValid(r *http.Request) bool {
	u, ok := w.Sessions.userCookie(r)
	return ok && w.Store != nil && w.Store.adminSessionValid(u.ID, u.AuthVersion)
}

func (s *readerSessions) scopedCookie(r *http.Request) ([32]byte, bool) {
	if s == nil || !secureBrowserRequest(r) {
		return [32]byte{}, false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || len(c.Value) != 64 {
		return [32]byte{}, false
	}
	hash := sha256.Sum256([]byte(c.Value))
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.active[hash]
	if !ok || !expiry.After(time.Now()) {
		delete(s.active, hash)
		delete(s.scoped, hash)
		delete(s.users, hash)
		return [32]byte{}, false
	}
	scope, ok := s.scoped[hash]
	return scope, ok
}
func (w Wiki) sessionAPI(rw http.ResponseWriter, r *http.Request) {
	sessions, cookie, token, prefix := w.Sessions, sessionCookie, w.ReaderToken, "/api/session/"
	if strings.HasPrefix(r.URL.Path, "/api/admin/session/") {
		sessions, cookie, token, prefix = w.AdminSessions, adminSessionCookie, w.Token, "/api/admin/session/"
	}
	if strings.HasPrefix(r.URL.Path, "/api/access/session/") {
		sessions, cookie, token, prefix = w.AccessSessions, accessSessionCookie, w.Token, "/api/access/session/"
	}
	if w.Public || sessions == nil || !secureBrowserRequest(r) {
		http.NotFound(rw, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == prefix+"status" {
		valid := sessions.validCookie(r, cookie)
		if prefix != "/api/session/" {
			valid = w.adminCookieValid(r, sessions, cookie)
		}
		if !valid && prefix == "/api/session/" && w.Store != nil {
			if hash, ok := sessions.scopedCookie(r); ok {
				valid = w.Store.validScopeHash(hash, time.Now())
			}
			if user, ok := sessions.userCookie(r); ok {
				valid = w.Store.userSessionValid(user.ID, user.AuthVersion)
			}
		}
		result := map[string]bool{"authenticated": valid}
		if prefix != "/api/session/" {
			result["via_reader_session"] = valid && w.readerAdminCookieValid(r)
		}
		jsonResponse(rw, 200, result)
		return
	}
	if r.Method == "GET" && prefix == "/api/session/" && r.URL.Path == prefix+"profile" {
		user, ok := sessions.userCookie(r)
		jsonResponse(rw, 200, map[string]bool{"password_change": ok && w.Store != nil && w.Store.userSessionValid(user.ID, user.AuthVersion)})
		return
	}
	if r.Method != "POST" {
		rw.WriteHeader(405)
		return
	}
	if !sameOrigin(r) {
		jsonResponse(rw, 403, map[string]string{"error": "origin rejected"})
		return
	}
	if prefix == "/api/admin/session/" && r.URL.Path == prefix+"setup" {
		var in struct {
			Username string `json:"username"`
			Token    string `json:"setup_token"`
			Password string `json:"password"`
		}
		if decodeLimited(rw, r, &in, 2048) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		sessions.mu.Lock()
		allowed := sessions.allowAttempt(time.Now())
		sessions.mu.Unlock()
		if !allowed {
			jsonResponse(rw, 429, map[string]string{"error": "try again later"})
			return
		}
		if w.Store == nil {
			http.NotFound(rw, r)
			return
		}
		if err := w.Store.finishAdminSetup(in.Username, in.Token, in.Password); err != nil {
			apiError(rw, err)
			return
		}
		jsonResponse(rw, 200, map[string]bool{"configured": true})
		return
	}
	if r.URL.Path == prefix+"logout" {
		clear := func(store *readerSessions, name string) {
			if store != nil {
				if c, e := r.Cookie(name); e == nil {
					hash := sha256.Sum256([]byte(c.Value))
					store.mu.Lock()
					delete(store.active, hash)
					delete(store.scoped, hash)
					delete(store.users, hash)
					store.mu.Unlock()
				}
			}
			http.SetCookie(rw, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
		}
		clear(sessions, cookie)
		if prefix == "/api/session/" {
			clear(w.AdminSessions, adminSessionCookie)
			clear(w.AccessSessions, accessSessionCookie)
		}
		jsonResponse(rw, 200, map[string]bool{"authenticated": false})
		return
	}
	if prefix == "/api/session/" && r.URL.Path == prefix+"password" {
		user, ok := sessions.userCookie(r)
		if !ok || w.Store == nil {
			http.NotFound(rw, r)
			return
		}
		var in struct {
			Current string `json:"current_password"`
			New     string `json:"new_password"`
		}
		if decodeLimited(rw, r, &in, 2048) != nil {
			apiError(rw, ErrInvalid)
			return
		}
		sessions.mu.Lock()
		allowed := sessions.allowAttempt(time.Now())
		sessions.mu.Unlock()
		if !allowed {
			jsonResponse(rw, 429, map[string]string{"error": "try again later"})
			return
		}
		version, valid := w.Store.authenticateUser(user.ID, in.Current)
		if !valid || version != user.AuthVersion {
			jsonResponse(rw, 403, map[string]string{"error": "access denied"})
			return
		}
		w.Store.mu.RLock()
		current, exists := w.Store.db.Users[user.ID]
		w.Store.mu.RUnlock()
		if !exists || current.AuthVersion != user.AuthVersion {
			apiError(rw, ErrConflict)
			return
		}
		if _, err := w.Store.saveUser(user.ID, nil, &in.New, nil, &current.Revision); err != nil {
			apiError(rw, err)
			return
		}
		if c, e := r.Cookie(cookie); e == nil {
			hash := sha256.Sum256([]byte(c.Value))
			sessions.mu.Lock()
			delete(sessions.active, hash)
			delete(sessions.users, hash)
			delete(sessions.scoped, hash)
			sessions.mu.Unlock()
		}
		http.SetCookie(rw, &http.Cookie{Name: cookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
		jsonResponse(rw, 200, map[string]bool{"authenticated": false})
		return
	}
	if r.URL.Path != prefix+"login" {
		http.NotFound(rw, r)
		return
	}
	var in struct {
		Key      string `json:"key"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if decodeLimited(rw, r, &in, 2048) != nil {
		apiError(rw, ErrInvalid)
		return
	}
	now := time.Now()
	sessions.mu.Lock()
	if !sessions.allowAttempt(now) {
		sessions.mu.Unlock()
		jsonResponse(rw, 429, map[string]string{"error": "try again later"})
		return
	}
	fullAccess := in.Username == "" && in.Password == "" && token != "" && subtle.ConstantTimeCompare([]byte(in.Key), []byte(token)) == 1
	var scopedHash [32]byte
	scopedAccess := false
	if !fullAccess && in.Username == "" && in.Password == "" && prefix == "/api/session/" && w.Store != nil && w.Store.accessScope(in.Key, now) != nil {
		scopedHash = sha256.Sum256([]byte(in.Key))
		scopedAccess = true
	}
	var person userSession
	userAccess := false
	if !fullAccess && !scopedAccess && in.Key == "" && w.Store != nil {
		person.ID = in.Username
		person.AuthVersion, userAccess = w.Store.authenticateUser(in.Username, in.Password)
		if userAccess && prefix != "/api/session/" {
			userAccess = w.Store.adminSessionValid(person.ID, person.AuthVersion)
		}
	}
	if !fullAccess && !scopedAccess && !userAccess {
		sessions.mu.Unlock()
		jsonResponse(rw, 403, map[string]string{"error": "access denied"})
		return
	}
	for h, expiry := range sessions.active {
		scope, restricted := sessions.scoped[h]
		user, isUser := sessions.users[h]
		if !expiry.After(now) || (restricted && (w.Store == nil || !w.Store.validScopeHash(scope, now))) || (isUser && (w.Store == nil || !w.Store.userSessionValid(user.ID, user.AuthVersion))) {
			delete(sessions.active, h)
			delete(sessions.scoped, h)
			delete(sessions.users, h)
		}
	}
	if len(sessions.active) >= 100 {
		sessions.mu.Unlock()
		jsonResponse(rw, 429, map[string]string{"error": "session limit"})
		return
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		sessions.mu.Unlock()
		apiError(rw, err)
		return
	}
	value := hex.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(value))
	if sessions.active == nil {
		sessions.active = map[[32]byte]time.Time{}
	}
	sessions.active[hash] = now.Add(8 * time.Hour)
	if scopedAccess {
		if sessions.scoped == nil {
			sessions.scoped = map[[32]byte][32]byte{}
		}
		sessions.scoped[hash] = scopedHash
	}
	if userAccess {
		if sessions.users == nil {
			sessions.users = map[[32]byte]userSession{}
		}
		sessions.users[hash] = person
	}
	sessions.mu.Unlock()
	http.SetCookie(rw, &http.Cookie{Name: cookie, Value: value, Path: "/", MaxAge: 8 * 60 * 60, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	jsonResponse(rw, 200, map[string]bool{"authenticated": true})
}
