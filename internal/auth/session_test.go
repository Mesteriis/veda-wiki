package auth

import (
	"strconv"
	"testing"
	"time"
)

func TestLoginAttemptTableIsBounded(t *testing.T) {
	s := &Sessions{}
	now := time.Now()
	for i := 0; i < attemptIdentityMax; i++ {
		if !s.AllowAttempt("user", strconv.Itoa(i), now) {
			t.Fatal("identity rejected before the table was full", i)
		}
	}
	if s.AllowAttempt("user", "new", now) {
		t.Fatal("full table accepted a new identity")
	}
	if !s.AllowAttempt("user", "0", now) {
		t.Fatal("full table locked out a known identity below its limit")
	}
	if !s.AllowAttempt("user", "new", now.Add(2*time.Minute)) || len(s.attempts) != 1 {
		t.Fatal("expired identities were not pruned", len(s.attempts))
	}
}
