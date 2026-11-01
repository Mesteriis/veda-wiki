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
	if s == nil || !secureBrowserRequest(r) {
		return false
	}
	c, err := r.Cookie(sessionCookie)
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
	if w.Public || w.Sessions == nil || !secureBrowserRequest(r) {
		http.NotFound(rw, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/api/session/status" {
		jsonResponse(rw, 200, map[string]bool{"authenticated": w.Sessions.valid(r)})
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
	if r.URL.Path == "/api/session/logout" {
		if c, e := r.Cookie(sessionCookie); e == nil {
			hash := sha256.Sum256([]byte(c.Value))
			w.Sessions.mu.Lock()
			delete(w.Sessions.active, hash)
			w.Sessions.mu.Unlock()
		}
		http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
		jsonResponse(rw, 200, map[string]bool{"authenticated": false})
		return
	}
	if r.URL.Path != "/api/session/login" {
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
	w.Sessions.mu.Lock()
	kept := w.Sessions.attempts[:0]
	for _, t := range w.Sessions.attempts {
		if t.After(now.Add(-time.Minute)) {
			kept = append(kept, t)
		}
	}
	w.Sessions.attempts = kept
	if len(kept) >= 10 {
		w.Sessions.mu.Unlock()
		jsonResponse(rw, 429, map[string]string{"error": "try again later"})
		return
	}
	w.Sessions.attempts = append(w.Sessions.attempts, now)
	if w.ReaderToken == "" || subtle.ConstantTimeCompare([]byte(in.Key), []byte(w.ReaderToken)) != 1 {
		w.Sessions.mu.Unlock()
		jsonResponse(rw, 403, map[string]string{"error": "access denied"})
		return
	}
	for h, expiry := range w.Sessions.active {
		if !expiry.After(now) {
			delete(w.Sessions.active, h)
		}
	}
	if len(w.Sessions.active) >= 100 {
		w.Sessions.mu.Unlock()
		jsonResponse(rw, 429, map[string]string{"error": "session limit"})
		return
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		w.Sessions.mu.Unlock()
		apiError(rw, err)
		return
	}
	value := hex.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(value))
	if w.Sessions.active == nil {
		w.Sessions.active = map[[32]byte]time.Time{}
	}
	w.Sessions.active[hash] = now.Add(8 * time.Hour)
	w.Sessions.mu.Unlock()
	http.SetCookie(rw, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", MaxAge: 8 * 60 * 60, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	jsonResponse(rw, 200, map[string]bool{"authenticated": true})
}
