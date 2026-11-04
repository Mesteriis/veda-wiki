package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

const passwordMemoryKiB = 19 * 1024

// passwordWorkers bounds simultaneous Argon2id derivations process-wide, and
// with them the memory that logins and password changes can use
// (passwordWorkers × 19 MiB).
const passwordWorkers = 2

// passwordSlots holds one token per running derivation.
var passwordSlots = make(chan struct{}, passwordWorkers)

// passwordKey is the Argon2id derivation. Tests replace it to observe how many
// derivations run at once.
var passwordKey = func(password, salt []byte) []byte {
	return argon2.IDKey(password, salt, 2, passwordMemoryKiB, 1, 32)
}

// PasswordValid reports whether p is an acceptable password or setup token.
func PasswordValid(p string) bool {
	n := utf8.RuneCountInString(p)
	return utf8.ValidString(p) && len(p) <= 512 && n >= 10 && n <= 128
}

// derive computes the Argon2id key of password with salt once one of the
// passwordWorkers slots is free. It returns ctx's error, without deriving,
// when ctx ends first.
func derive(ctx context.Context, password string, salt []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case passwordSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-passwordSlots }()
	return passwordKey([]byte(password), salt), nil
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
	derived, err := derive(context.Background(), password, salt)
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(salt), hex.EncodeToString(derived), nil
}
