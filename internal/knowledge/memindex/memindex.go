// Package memindex is the in-memory knowledge index: deterministic, used by
// make test and by the non-persistent development mode, and the reference
// behaviour every other knowledge.Index must match. Each read first builds
// a view of only the snapshots in the request's allowed set under the active
// fingerprint and computes everything from that view, so evidence outside
// the set cannot influence names, aliases, counts, ranking or traversal.
package memindex

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
)

// Index is an in-memory knowledge.Index. The zero value is not usable; use New.
type Index struct {
	mu    sync.RWMutex
	snaps map[knowledge.Snapshot]knowledge.Graph
	down  error
}

var _ knowledge.Index = (*Index)(nil)

// New returns an empty index.
func New() *Index { return &Index{snaps: map[knowledge.Snapshot]knowledge.Graph{}} }

// SetUnavailable makes every call fail with err until it is called with
// nil. It simulates an index outage in tests.
func (ix *Index) SetUnavailable(err error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.down = err
}

func (ix *Index) unavailable() error {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.down
}

// Publish implements knowledge.Index: the graph is deep-copied and replaces
// any previous graph of snap in one step under the lock.
func (ix *Index) Publish(ctx context.Context, snap knowledge.Snapshot, g knowledge.Graph) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(g)
	if err != nil {
		return err
	}
	var copied knowledge.Graph
	if err = json.Unmarshal(data, &copied); err != nil {
		return err
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.down != nil {
		return ix.down
	}
	ix.snaps[snap] = copied
	return nil
}

// Retire implements knowledge.Index.
func (ix *Index) Retire(ctx context.Context, snaps []knowledge.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.down != nil {
		return ix.down
	}
	for _, s := range snaps {
		delete(ix.snaps, s)
	}
	return nil
}

// Snapshots implements knowledge.Index.
func (ix *Index) Snapshots(ctx context.Context) ([]knowledge.Snapshot, error) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if ix.down != nil {
		return nil, ix.down
	}
	out := make([]knowledge.Snapshot, 0, len(ix.snaps))
	for s := range ix.snaps {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return snapLess(out[i], out[j]) })
	return out, nil
}

// Status implements knowledge.Index.
func (ix *Index) Status(ctx context.Context) error { return ix.unavailable() }

func keyLess(a, b knowledge.SnapshotKey) bool {
	if a.SourceID != b.SourceID {
		return a.SourceID < b.SourceID
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.SHA < b.SHA
}

func snapLess(a, b knowledge.Snapshot) bool {
	if a.Key != b.Key {
		return keyLess(a.Key, b.Key)
	}
	return a.Fingerprint < b.Fingerprint
}

func evidenceLess(a, b knowledge.Evidence) bool {
	if a.Key() != b.Key() {
		return keyLess(a.Key(), b.Key())
	}
	if a.BlockID != b.BlockID {
		return a.BlockID < b.BlockID
	}
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	return a.End < b.End
}
