package store

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Blob collection removes image files that nothing in the database
// references. A blob is removed only when it is unreferenced and its
// modification time is older than blobGrace. WriteBlobs refreshes that time
// even when the blob already exists, so a blob written for a commit that has
// not happened yet stays protected for the whole grace period; blobMu keeps a
// sweep from interleaving with WriteBlobs.
const (
	blobGrace           = 24 * time.Hour
	blobCollectInterval = 5 * time.Minute
	blobTempPrefix      = ".blob-"
)

var errBlobCollection = errors.New("blob collection incomplete")

// blobSweep counts the outcome of one collection; only counts are logged.
type blobSweep struct {
	blobs, temporary, skipped, failed int
}

// now is the store's clock for scheduling collections. Tests replace it.
func (s *Store) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// CollectBlobs removes, under the write lock, every regular file in the blobs
// directory named by a digest the database does not reference, and every
// temporary blob file, whose modification time is more than 24 hours before
// now. It returns the number of files removed. It refuses read-only, failed
// (Invalid), pre-v21 and unvalidated stores.
func (s *Store) CollectBlobs(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.collectBlobs(now)
}

// collectBlobsDue runs a collection after a successful commit or at Open
// unless one ran within blobCollectInterval. Failures are only logged: the
// commit that triggered it has already succeeded. The caller holds the lock.
func (s *Store) collectBlobsDue() {
	now := s.now()
	if !s.blobsCollected.IsZero() && now.Sub(s.blobsCollected) < blobCollectInterval {
		return
	}
	if s.collectable() != nil {
		return
	}
	// collectBlobs logs failures as counts; the error is intentionally not
	// returned, because the commit or Open that triggered it has succeeded.
	s.collectBlobs(now)
}

// collectable reports why the store must not collect blobs, if it must not.
func (s *Store) collectable() error {
	switch {
	case s.readOnly:
		return errReadOnly
	case s.invalid:
		return errors.New("store unavailable")
	case s.db.Version < domain.RevisionsFormat:
		return errors.New("blob collection requires the current store format")
	}
	return nil
}

// collectBlobs is CollectBlobs for a caller that holds the write lock.
func (s *Store) collectBlobs(now time.Time) (int, error) {
	if err := s.collectable(); err != nil {
		return 0, err
	}
	s.blobsCollected = now
	// Validate fills defaults in place, so it checks a private clone.
	check := clone(&s.db)
	if Validate(&check) != nil {
		log.Printf("blob collection skipped: store failed validation")
		return 0, errors.New("store failed validation")
	}
	s.blobMu.Lock()
	sweep := sweepBlobs(filepath.Join(s.dir, "blobs"), domain.ReferencedBlobs(check), now)
	s.blobMu.Unlock()
	if sweep != (blobSweep{}) {
		log.Printf("blob collection: removed %d blobs and %d temporary files; skipped %d entries; %d failures", sweep.blobs, sweep.temporary, sweep.skipped, sweep.failed)
	}
	removed := sweep.blobs + sweep.temporary
	if sweep.failed > 0 {
		return removed, errBlobCollection
	}
	return removed, nil
}

// sweepBlobs removes unreferenced digest-named blobs and temporary blob files
// older than the grace period. Only plain regular files are candidates;
// symbolic links, directories and other names are skipped and counted. A
// blobs directory that is not a real directory is not entered.
func sweepBlobs(dir string, refs map[string]bool, now time.Time) blobSweep {
	var sweep blobSweep
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return sweep
	}
	if err != nil || !info.IsDir() {
		sweep.skipped, sweep.failed = 1, 1
		return sweep
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		sweep.failed = 1
		return sweep
	}
	cutoff := now.Add(-blobGrace)
	for _, entry := range entries {
		name := entry.Name()
		_, notDigest := domain.HexKey(name)
		temporary := strings.HasPrefix(name, blobTempPrefix)
		if notDigest != nil && !temporary {
			sweep.skipped++
			continue
		}
		if notDigest == nil && refs[name] {
			continue
		}
		file := filepath.Join(dir, name)
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() {
			sweep.skipped++
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err = os.Remove(file); err != nil {
			sweep.failed++
			continue
		}
		if temporary {
			sweep.temporary++
		} else {
			sweep.blobs++
		}
	}
	if sweep.blobs+sweep.temporary > 0 && syncDir(dir) != nil {
		sweep.failed++
	}
	return sweep
}
