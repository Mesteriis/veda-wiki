package domain

import (
	"time"
)

// SourcePublic reports whether src is public now: a public local source, or
// a public source whose connection is active, public and recently verified.
func SourcePublic(db Database, src Source, now time.Time) bool {
	if src.Visibility != "public" {
		return false
	}
	if src.ConnectionID == "" {
		return true
	}
	c, ok := db.Connections[src.ConnectionID]
	verified, e := time.Parse(time.RFC3339Nano, c.VerifiedAt)
	return ok && !c.Paused && c.Verification == "verified" && !c.Private && e == nil && !verified.After(now.Add(time.Minute)) && now.Sub(verified) < 10*time.Minute
}

// A partial import cannot establish which previously imported paths remain
// public, regardless of whether the source is local or connected to GitHub.
// Publication decisions still use SourcePublic, but readers fail closed until
// the import is complete. Sources without an import are not partial.
func SourcePublicReadable(db Database, src Source, now time.Time) bool {
	if src.ConnectionID != "" {
		c := db.Connections[src.ConnectionID]
		// A visibility check alone does not establish which head was imported.
		// Configuration changes clear the recorded head until a fresh pull.
		// A sync of an unchanged head keeps serving the snapshot under its
		// previous proof; connectionStatusFor does not refresh it at sync start.
		// A newer observed head withholds the snapshot until it is imported.
		if (c.PendingHead != "" && c.PendingHead != c.LastPull) || !commitIDPattern.MatchString(c.LastPull) || c.LastPull != src.ImportCommit || !commitIDPattern.MatchString(c.TreeSHA) {
			return false
		}
	}
	return SourcePublic(db, src, now) && !src.ImportPartial
}

// The caller holds the store lock or operates on a request-local snapshot.
// A blank value means no current sync warning, not proof of live correctness.
func SourceSyncIssue(db Database, src Source, now time.Time) string {
	if src.ConnectionID == "" {
		return ""
	}
	conn, exists := db.Connections[src.ConnectionID]
	switch {
	case !exists:
		return "unverified"
	case conn.Paused:
		return "paused"
	case conn.State == "error":
		return "error"
	case conn.State == "blocked" || conn.State == "conflict":
		return "attention"
	case conn.State == "syncing":
		return "checking"
	default:
		verified, err := time.Parse(time.RFC3339Nano, conn.VerifiedAt)
		if conn.Verification != "verified" ||
			err != nil ||
			verified.After(now.Add(time.Minute)) ||
			now.Sub(verified) >= 10*time.Minute ||
			!commitIDPattern.MatchString(conn.LastPull) ||
			conn.LastPull != src.ImportCommit ||
			!commitIDPattern.MatchString(conn.TreeSHA) {
			return "unverified"
		}
	}
	return ""
}
