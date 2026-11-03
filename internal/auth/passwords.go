package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

const passwordMemoryKiB = 19 * 1024

// passwordMu serializes Argon2id derivations process-wide, bounding the
// memory that simultaneous logins and password changes can use.
var passwordMu sync.Mutex

// PasswordValid reports whether p is an acceptable password or setup token.
func PasswordValid(p string) bool {
	n := utf8.RuneCountInString(p)
	return utf8.ValidString(p) && len(p) <= 512 && n >= 10 && n <= 128
}

// derive computes the Argon2id key of password with salt under passwordMu.
func derive(password string, salt []byte) []byte {
	passwordMu.Lock()
	defer passwordMu.Unlock()
	return argon2.IDKey([]byte(password), salt, 2, passwordMemoryKiB, 1, 32)
}

// passwordHash returns a new random salt and the derived key, hex-encoded.
func passwordHash(password string) (string, string, error) {
	if !PasswordValid(password) {
		return "", "", domain.ErrInvalid
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", "", err
	}
	derived := derive(password, salt)
	return hex.EncodeToString(salt), hex.EncodeToString(derived), nil
}
