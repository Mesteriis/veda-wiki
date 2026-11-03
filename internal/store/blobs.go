package store

import (
	"io"
	"os"
	"path"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Blobs are immutable image files named by their SHA-256 digest under
// dir/blobs. They are written before any record references them.

func (s *Store) readBlob(sha string) ([]byte, domain.BlobRef, error) {
	if _, err := domain.HexKey(sha); err != nil {
		return nil, domain.BlobRef{}, domain.ErrInvalid
	}
	filename := path.Join(s.dir, "blobs", sha)
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

// WriteBlob stores an image durably and returns its reference. An existing
// valid blob with the same digest is kept. A blob may remain unreferenced if
// a later commit fails, but a referenced digest never holds a partial file.
func (s *Store) WriteBlob(data []byte) (domain.BlobRef, error) {
	ref, err := domain.ImageRef(data)
	if err != nil {
		return domain.BlobRef{}, err
	}
	if s.VerifyBlob(ref) == nil {
		return ref, nil
	}
	directory := path.Join(s.dir, "blobs")
	if err = os.MkdirAll(directory, 0700); err != nil {
		return domain.BlobRef{}, err
	}
	f, err := os.CreateTemp(directory, ".blob-")
	if err != nil {
		return domain.BlobRef{}, err
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
		return domain.BlobRef{}, err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return domain.BlobRef{}, err
	}
	defer dir.Close()
	return ref, dir.Sync()
}
