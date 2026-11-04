package store

import (
	"crypto/rand"
	"encoding/hex"
)

// RandomID returns 32 lowercase hex characters from the operating system's
// random source. It names move operations and credential files; the value
// grants nothing by itself.
func RandomID() (string, error) {
	b := make([]byte, 16)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
