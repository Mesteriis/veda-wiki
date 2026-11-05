package notify

import (
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits on the public request form.
const (
	PerAddressPerHour = 3
	TotalPerDay       = 20
	MaxComment        = 1000
	MaxSources        = 20
)

var telegramNick = regexp.MustCompile(`^@[A-Za-z0-9_]{5,32}$`)

// ValidNick reports whether nick is a Telegram username with its @.
func ValidNick(nick string) bool { return telegramNick.MatchString(nick) }

// CleanComment trims a comment and reports whether it fits the limits. It
// rejects control characters other than newlines and tabs.
func CleanComment(comment string) (string, bool) {
	comment = strings.TrimSpace(comment)
	if !utf8.ValidString(comment) || utf8.RuneCountInString(comment) > MaxComment {
		return "", false
	}
	for _, r := range comment {
		// Controls and invisible format characters (bidi overrides, zero
		// width) could disguise the text in the owner's message.
		if r < 0x20 && r != '\n' && r != '\t' || r >= 0x7f && r < 0xa0 || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	return comment, true
}

// Limiter enforces the per-address hourly and the global daily limit in
// memory. Addresses are never stored on disk.
type Limiter struct {
	mu      sync.Mutex
	address map[string][]time.Time
	total   []time.Time
}

// Allow records an attempt from address at now when it is within limits.
func (l *Limiter) Allow(address string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.address == nil {
		l.address = map[string][]time.Time{}
	}
	l.total = recent(l.total, now.Add(-24*time.Hour))
	for a, times := range l.address {
		if kept := recent(times, now.Add(-time.Hour)); len(kept) == 0 {
			delete(l.address, a)
		} else {
			l.address[a] = kept
		}
	}
	if len(l.total) >= TotalPerDay || len(l.address[address]) >= PerAddressPerHour {
		return false
	}
	l.total = append(l.total, now)
	l.address[address] = append(l.address[address], now)
	return true
}

func recent(times []time.Time, after time.Time) []time.Time {
	kept := times[:0]
	for _, t := range times {
		if t.After(after) {
			kept = append(kept, t)
		}
	}
	return kept
}
