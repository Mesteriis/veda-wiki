package main

import (
	"github.com/Mesteriis/veda-wiki/internal/importer"
)

// Import names for the cmd tests that remain until their areas move.

type (
	ImportBatch  = importer.Batch
	ImportResult = importer.Result
	remoteProof  = importer.RemoteProof
)

var (
	errImportedAssetUnavailable = importer.ErrAssetUnavailable
)

const maxImportRequestBytes = importer.MaxRequestBytes

func (s *Store) importBatch(b ImportBatch) (ImportResult, error) { return s.importer().Import(b) }

func (s *Store) applyImport(b ImportBatch, proof *remoteProof, localRoot bool) (ImportResult, error) {
	return s.importer().Apply(b, proof, localRoot)
}

func (s *Store) conflicts() []importer.ConflictRecord { return s.importer().Conflicts() }

func (s *Store) resolveConflict(sourceID, p string, index, expected int, resolution string) error {
	return s.importer().ResolveConflict(sourceID, p, index, expected, resolution)
}

func (s *Store) removeLocalImportedSource(id, expectedGeneration string) (int, error) {
	return s.importer().RemoveLocalImportedSource(id, expectedGeneration)
}
