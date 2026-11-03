package auth

import (
	"crypto/sha256"
	"time"
)

const (
	attemptsPerIdentity = 10
	attemptPruneAt      = 1024
	attemptIdentityMax  = 4096
)

// AllowAttempt records one password or setup-token attempt for identity and
// reports whether it is within the limit.
func (s *Sessions) AllowAttempt(kind, identity string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allowAttempt(kind, identity, now)
}

// Caller holds the session mutex. Password and setup-token checks are limited
// per identity, including unknown identities, so failures against one name
// cannot lock out other accounts. Random 256-bit keys are not limited here.
// The identity table is bounded; only a flood of distinct names fills it.
func (s *Sessions) allowAttempt(kind, identity string, now time.Time) bool {
	bucket := sha256.Sum256([]byte(kind + "\x00" + identity))
	recent := func(times []time.Time) []time.Time {
		kept := times[:0]
		for _, t := range times {
			if t.After(now.Add(-time.Minute)) {
				kept = append(kept, t)
			}
		}
		return kept
	}
	if s.attempts == nil {
		s.attempts = map[[32]byte][]time.Time{}
	}
	if len(s.attempts) >= attemptPruneAt {
		for id, times := range s.attempts {
			if kept := recent(times); len(kept) == 0 {
				delete(s.attempts, id)
			} else {
				s.attempts[id] = kept
			}
		}
	}
	kept := recent(s.attempts[bucket])
	if len(kept) >= attemptsPerIdentity {
		s.attempts[bucket] = kept
		return false
	}
	if len(kept) == 0 {
		delete(s.attempts, bucket)
		if len(s.attempts) >= attemptIdentityMax {
			return false
		}
	}
	s.attempts[bucket] = append(kept, now)
	return true
}
