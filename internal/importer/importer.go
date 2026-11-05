// Package importer applies observed repository content to the store: veda.yaml
// metadata, bounded local checkout observations, batch decoding, asset
// storage, import conflicts and the process-local import observations.
package importer

import (
	"context"
	"errors"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// Bound aggregate imported content separately from the JSON transport. Images
// are base64-encoded in transport, and JSON text may require escaping.
const (
	MaxBytes        = 32 << 20
	MaxRequestBytes = 64 << 20
)

// ErrAssetUnavailable reports that an image the import would reference is
// missing or corrupt in the blob store.
var ErrAssetUnavailable = errors.New("imported asset unavailable")

// Batch is one observation of a source's repository content.
type Batch struct {
	withheldReasons map[string]int       // local preview only; never serialized
	SourceID        string               `json:"source_id"`
	Kind            string               `json:"kind"`
	Metadata        *domain.WikiMetadata `json:"metadata,omitempty"`
	Files           map[string]string    `json:"files"`
	Assets          map[string][]byte    `json:"assets,omitempty"`
	Commit          string               `json:"commit"`
	Dirty           bool                 `json:"dirty"`
	Complete        bool                 `json:"complete"`
	Withheld        int                  `json:"withheld"`
	WithheldPaths   map[string]bool      `json:"withheld_paths,omitempty"`
	ExpectedImport  *string              `json:"expected_import"`
	Create          bool                 `json:"create"`
	Visibility      string               `json:"visibility,omitempty"`
}

// WithheldReasons counts withheld checkout files by reason. Only
// PrepareCheckout fills it; it is never serialized.
func (b Batch) WithheldReasons() map[string]int { return b.withheldReasons }

// ObservedTree reports whether the observation saw every path of the tree,
// so that an imported article absent from Files was deleted upstream: either
// nothing was withheld, or every withheld file is named in WithheldPaths. A
// withheld file still leaves the import partial; only Complete without
// withheld files proves that nothing was left out.
func (b Batch) ObservedTree() bool {
	if b.Withheld == 0 {
		return b.Complete
	}
	return len(b.WithheldPaths) == b.Withheld
}

// Result counts what an import changed.
type Result struct {
	Generation string `json:"generation"`
	Created    int    `json:"created"`
	Updated    int    `json:"updated"`
	Deleted    int    `json:"deleted"`
	Conflicts  int    `json:"conflicts"`
	Unchanged  int    `json:"unchanged"`
	Withheld   int    `json:"withheld"`
}

// RemoteProof ties a batch to the GitHub connection state it was fetched
// under. Apply rejects it if the connection changed since.
type RemoteProof struct {
	ConnectionID   string
	ConfigRevision int
	Visibility     string
	TreeSHA        string
	Adoption       *AdoptionProof
	// Record runs inside the import transaction once the content is applied
	// and moves are reconciled. The synchronizer records the pull there, so
	// the observed head commits atomically with the content. It is required.
	Record func(db *domain.Database)
}

// AdoptionProof is the local state a private source had when a connection
// was configured to adopt it.
type AdoptionProof struct {
	Connection domain.GitHubConnection
	Account    domain.GitHubAccount
	Snapshot   string
	Context    context.Context
	// Attach runs inside the import transaction after the adoption checks and
	// before any article is compared; the synchronizer attaches Connection.
	// It is required.
	Attach func(db *domain.Database)
}

// Service runs imports against a store and records their observations.
type Service struct {
	Store        *store.Store
	Observations *Observations
}
