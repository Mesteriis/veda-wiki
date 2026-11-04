package store

import (
	"io"
	"os"
	"path"
	"time"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Blobs are immutable image files named by their SHA-256 digest under
// dir/blobs. They are written before any record references them.

// BlobPath returns the file that holds the image blob with digest sha. The
// digest is not validated; ReadBlob and VerifyBlob check the content.
func (s *Store) BlobPath(sha string) string {
	return path.Join(s.dir, "blobs", sha)
}

func (s *Store) readBlob(sha string) ([]byte, domain.BlobRef, error) {
	if _, err := domain.HexKey(sha); err != nil {
		return nil, domain.BlobRef{}, domain.ErrInvalid
	}
	filename := s.BlobPath(sha)
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return nil, domain.BlobRef{}, domain.ErrInvalid
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, domain.BlobRef{}, domain.ErrNotFound
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	verified, check := domain.ImageRef(content)
	if err != nil || check != nil || verified.SHA != sha {
		return nil, domain.BlobRef{}, domain.ErrInvalid
	}
	return content, verified, nil
}

// ReadBlob returns the stored image with digest sha after verifying it.
func (s *Store) ReadBlob(sha string) ([]byte, error) {
	content, _, err := s.readBlob(sha)
	return content, err
}

// VerifyBlob checks that the blob exists and matches ref's digest and type.
func (s *Store) VerifyBlob(ref domain.BlobRef) error {
	_, verified, err := s.readBlob(ref.SHA)
	if err != nil {
		return err
	}
	if verified != ref {
		return domain.ErrInvalid
	}
	return nil
}

// VerifyBlobs checks each distinct blob of refs once with VerifyBlob and
// returns the first failure.
func (s *Store) VerifyBlobs(refs map[string]domain.BlobRef) error {
	seen := map[domain.BlobRef]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		if err := s.VerifyBlob(ref); err != nil {
			return err
		}
		seen[ref] = true
	}
	return nil
}

// syncDir makes renames in dir durable. Tests replace it to count syncs.
var syncDir = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// writeBlob writes, fsyncs and renames one image into directory unless a
// valid blob with the same digest exists; an existing blob's modification
// time is set to now so that blob collection keeps it for the grace period
// until the commit that references it. It does not sync the directory.
func (s *Store) writeBlob(directory string, data []byte) (domain.BlobRef, bool, error) {
	ref, err := domain.ImageRef(data)
	if err != nil {
		return domain.BlobRef{}, false, err
	}
	if s.VerifyBlob(ref) == nil {
		now := time.Now()
		if err = os.Chtimes(s.BlobPath(ref.SHA), now, now); err != nil {
			return domain.BlobRef{}, false, err
		}
		return ref, false, nil
	}
	f, err := os.CreateTemp(directory, ".blob-")
	if err != nil {
		return domain.BlobRef{}, false, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path.Join(directory, ref.SHA))
	}
	os.Remove(f.Name())
	if err != nil {
		return domain.BlobRef{}, false, err
	}
	return ref, true, nil
}

// WriteBlobs stores images durably and returns their references by the same
// keys. Existing valid blobs are kept and their modification time refreshed;
// the blobs directory is synced once, only if something was written. A blob
// may remain unreferenced if a later commit fails, but a referenced digest
// never holds a partial file. The caller must commit the references within
// 24 hours (blobGrace), or blob collection may remove the files.
func (s *Store) WriteBlobs(blobs map[string][]byte) (map[string]domain.BlobRef, error) {
	refs := make(map[string]domain.BlobRef, len(blobs))
	if len(blobs) == 0 {
		return refs, nil
	}
	s.blobMu.Lock()
	defer s.blobMu.Unlock()
	directory := path.Join(s.dir, "blobs")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	changed := false
	for name, data := range blobs {
		ref, wrote, err := s.writeBlob(directory, data)
		if err != nil {
			return nil, err
		}
		refs[name] = ref
		changed = changed || wrote
	}
	if !changed {
		return refs, nil
	}
	return refs, syncDir(directory)
}
