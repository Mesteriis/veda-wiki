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

type readerSessions struct {
	mu       sync.Mutex
	active   map[[32]byte]time.Time
	attempts []time.Time
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
		return false
	}
	return true
}
func (w Wiki) sessionAPI(rw http.ResponseWriter, r *http.Request) {
	sessions, cookie, token, prefix := w.Sessions, sessionCookie, w.ReaderToken, "/api/session/"
	if strings.HasPrefix(r.URL.Path, "/api/admin/session/") {
		sessions, cookie, token, prefix = w.AdminSessions, adminSessionCookie, w.Token, "/api/admin/session/"
	}
	if w.Public || sessions == nil || !secureBrowserRequest(r) {
		http.NotFound(rw, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == prefix+"status" {
		jsonResponse(rw, 200, map[string]bool{"authenticated": sessions.validCookie(r, cookie)})
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
	if r.URL.Path == prefix+"logout" {
		if c, e := r.Cookie(cookie); e == nil {
			hash := sha256.Sum256([]byte(c.Value))
			sessions.mu.Lock()
			delete(sessions.active, hash)
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
		Key string `json:"key"`
	}
	if decodeLimited(rw, r, &in, 256) != nil {
		apiError(rw, ErrInvalid)
		return
	}
	now := time.Now()
	sessions.mu.Lock()
	kept := sessions.attempts[:0]
	for _, t := range sessions.attempts {
		if t.After(now.Add(-time.Minute)) {
			kept = append(kept, t)
		}
	}
	sessions.attempts = kept
	if len(kept) >= 10 {
		sessions.mu.Unlock()
		jsonResponse(rw, 429, map[string]string{"error": "try again later"})
		return
	}
	sessions.attempts = append(sessions.attempts, now)
	if token == "" || subtle.ConstantTimeCompare([]byte(in.Key), []byte(token)) != 1 {
		sessions.mu.Unlock()
		jsonResponse(rw, 403, map[string]string{"error": "access denied"})
		return
	}
	for h, expiry := range sessions.active {
		if !expiry.After(now) {
			delete(sessions.active, h)
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
	sessions.mu.Unlock()
	http.SetCookie(rw, &http.Cookie{Name: cookie, Value: value, Path: "/", MaxAge: 8 * 60 * 60, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	jsonResponse(rw, 200, map[string]bool{"authenticated": true})
}
