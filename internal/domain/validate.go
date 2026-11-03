package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// idPattern matches source, connection and other stored identifiers.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// commitIDPattern matches a full SHA-1 or SHA-256 Git object name.
var commitIDPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// credentialPattern matches text that looks like credential material.
var credentialPattern = regexp.MustCompile(`(?i)-----BEGIN .*PRIVATE KEY-----` +
	`|\b(?:password|passwd|api[_-]?key|access[_-]?token|client_secret)\s*[=:]\s*["']?[^\s"']{6,}` +
	`|\b(?:ghp_|github_pat_|sk-proj-)[A-Za-z0-9_\-]{12,}` +
	`|https?://[^\s/]+:[^\s/]+@`)

// ValidID reports whether s is a valid stored identifier.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// ValidCommitID reports whether s is a full Git object name.
func ValidCommitID(s string) bool { return commitIDPattern.MatchString(s) }

// CredentialLike reports whether s contains credential-like content.
func CredentialLike(s string) bool { return credentialPattern.MatchString(s) }

// CredentialLikeBytes is CredentialLike for b without copying it to a string.
func CredentialLikeBytes(b []byte) bool { return credentialPattern.Match(b) }

// SafeArticlePath reports whether p is a clean relative Markdown path.
func SafeArticlePath(p string) bool {
	return p != "" &&
		!strings.HasPrefix(p, "/") &&
		path.Clean(p) == p &&
		p != ".." &&
		!strings.HasPrefix(p, "../") &&
		(strings.EqualFold(path.Ext(p), ".md") || strings.EqualFold(path.Ext(p), ".markdown"))
}

// ArticlePath reports whether p is a valid article path for a source of
// kind: safe, without hidden parts and, for programs, under docs/.
func ArticlePath(p string, kind string) bool {
	if !utf8.ValidString(p) || !SafeArticlePath(p) || len(p) > 512 || strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return kind != "program" || strings.HasPrefix(p, "docs/")
}

// SafeRelative reports whether p is a clean relative path without hidden
// parts.
func SafeRelative(p string) bool {
	if p == "" || !utf8.ValidString(p) || len(p) > 512 || path.Clean(p) != p || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

// Digest returns the hexadecimal SHA-256 of b.
func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// HexKey validates a 64-character lowercase hexadecimal value and returns it
// as bytes, without decoding it.
func HexKey(s string) ([]byte, error) {
	if len(s) != 64 {
		return nil, ErrInvalid
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return nil, ErrInvalid
		}
	}
	return []byte(s), nil
}
