package store

import "github.com/Mesteriis/veda-wiki/internal/domain"

// WriteBlob stores one image durably; see WriteBlobs.
func (s *Store) WriteBlob(data []byte) (domain.BlobRef, error) {
	refs, err := s.WriteBlobs(map[string][]byte{"": data})
	if err != nil {
		return domain.BlobRef{}, err
	}
	return refs[""], nil
}
