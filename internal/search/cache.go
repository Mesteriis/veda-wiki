package search

import (
	"strings"
	"sync"
	"unicode"
)

// cacheBytes bounds the lowercased bodies a Cache keeps. Past it the cache
// starts over, which also drops revisions that are no longer current.
const cacheBytes = 64 << 20

// Cache keeps the lowercased body of each revision by its SHA-256, so a
// revision is lowercased once rather than on every query. Revisions are
// immutable and the key is the digest of the text, so an entry never goes
// stale; a caller only ever gets back the lowercase of the text it passed.
type Cache struct {
	mu     sync.Mutex
	bodies map[string]string
	size   int
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{bodies: map[string]string{}} }

// lowered returns body lowercased rune by rune, as scoreWithScratch does.
// A nil cache or an empty sha computes it without caching.
func (c *Cache) lowered(sha, body string) string {
	if c == nil || sha == "" {
		return lowerBody(body)
	}
	c.mu.Lock()
	cached, ok := c.bodies[sha]
	c.mu.Unlock()
	if ok {
		return cached
	}
	lowered := lowerBody(body)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.size+len(lowered) > cacheBytes {
		c.bodies, c.size = map[string]string{}, 0
	}
	if _, ok := c.bodies[sha]; !ok && len(lowered) <= cacheBytes {
		c.bodies[sha] = lowered
		c.size += len(lowered)
	}
	return lowered
}

// lowerBody lowercases ASCII bytes directly and other text rune by rune,
// mapping invalid UTF-8 to U+FFFD like a range loop does.
func lowerBody(body string) string {
	for i := 0; i < len(body); i++ {
		if body[i] >= 0x80 {
			return strings.Map(unicode.ToLower, body)
		}
	}
	return strings.ToLower(body)
}
