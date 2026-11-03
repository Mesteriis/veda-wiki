package domain

import (
	"bytes"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"
)

// LegacyArticleBytes is the largest revision text stores before version 13
// accept; larger text raises the store version so older binaries refuse it.
const LegacyArticleBytes = 256000

// MaxArticleBytes bounds a stored revision, translation or conflict text.
const MaxArticleBytes = 1 << 20

// ImportablePath reports whether a repository path may enter a source of the
// given kind. Dependency and build output directories are never imported.
func ImportablePath(p, kind string) bool {
	if !SafeRelative(p) || (kind == "program" && !strings.HasPrefix(p, "docs/")) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "node_modules" || part == "vendor" || part == "dist" {
			return false
		}
	}
	return true
}

// RemoteAssetPath reports whether name is an importable image path.
func RemoteAssetPath(name, kind string) bool {
	if !ImportablePath(name, kind) {
		return false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// ImageRef identifies b by digest and sniffed type. Only bounded PNG, JPEG,
// WebP and GIF content is accepted.
func ImageRef(b []byte) (BlobRef, error) {
	if len(b) == 0 || len(b) > 4<<20 {
		return BlobRef{}, ErrInvalid
	}
	if mime := sniffImage(b); mime != "" {
		return BlobRef{SHA: Digest(b), MIME: mime}, nil
	}
	return BlobRef{}, ErrInvalid
}

// sniffImage reports the image type by magic bytes, with the same signatures
// as net/http.DetectContentType, which domain must not import.
func sniffImage(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\x0D\x0A\x1A\x0A")):
		return "image/png"
	case bytes.HasPrefix(b, []byte("\xFF\xD8\xFF")):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif"
	case len(b) >= 14 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:14], []byte("WEBPVP")):
		return "image/webp"
	}
	return ""
}

// ValidateMetadata checks wiki metadata read from veda.yaml or a stored source.
func ValidateMetadata(m *WikiMetadata) error {
	if m == nil || m.SchemaVersion != 1 || m.Kind != "wiki" || strings.TrimSpace(m.Title) == "" || len(m.Title) > 200 || len(m.Description) > 4000 || len(m.URL) > 2048 {
		return ErrInvalid
	}
	if m.URL != "" {
		u, e := url.Parse(m.URL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return ErrInvalid
		}
	}
	if m.Image != "" && !SafeRelative(m.Image) {
		return ErrInvalid
	}
	return nil
}

// ValidateAccessKey checks a stored access key against the known sources.
func ValidateAccessKey(id string, key AccessKey, sources map[string]Source) error {
	if !idPattern.MatchString(id) || key.ID != id || len(key.Hash) != 64 || len(key.Sources) == 0 || len(key.Sources) > 100 {
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

// ValidatePeople checks stored users and teams and their references.
func ValidatePeople(db Database) error {
	if (len(db.Users) > 0 || len(db.Teams) > 0) && db.Version < 15 {
		return errors.New("users and teams require store version 15")
	}
	for id, team := range db.Teams {
		if id != team.ID || !idPattern.MatchString(id) || team.Revision < 1 || team.Revision > 1000000000 || len(team.Sources) == 0 || len(team.Sources) > 100 {
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
		if id != user.ID || !idPattern.MatchString(id) || user.Revision < 1 || user.Revision > 1000000000 || user.AuthVersion < 1 || user.AuthVersion > 1000000000 || len(user.Teams) > 100 {
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
