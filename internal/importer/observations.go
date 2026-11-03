package importer

import (
	"sync"
	"time"
)

// Observations hold, per source, when an import last succeeded and how many
// files it withheld. They are process-local and lost on restart. The zero
// value is ready to use; a nil *Observations has no observations.
//
// Writers record an observation under the store write lock, after the commit
// that it describes, so its order matches the commit order. Readers that hold
// the store read lock may read it; Observations has its own mutex, always
// taken after the store lock.
type Observations struct {
	mu       sync.RWMutex
	observed map[string]time.Time
	withheld map[string]int
}

// Observed returns when an import of sourceID last succeeded.
func (o *Observations) Observed(sourceID string) (time.Time, bool) {
	if o == nil {
		return time.Time{}, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	t, ok := o.observed[sourceID]
	return t, ok
}

// Withheld returns how many files the last successful import of sourceID
// withheld. Counts are only known after a full import attempt, not after a
// head-only check.
func (o *Observations) Withheld(sourceID string) (int, bool) {
	if o == nil {
		return 0, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	n, ok := o.withheld[sourceID]
	return n, ok
}

// MarkObserved records a successful observation of sourceID at at.
func (o *Observations) MarkObserved(sourceID string, at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.observed == nil {
		o.observed = map[string]time.Time{}
	}
	o.observed[sourceID] = at
}

// MarkWithheld records the withheld count of a successful import.
func (o *Observations) MarkWithheld(sourceID string, count int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.withheld == nil {
		o.withheld = map[string]int{}
	}
	o.withheld[sourceID] = count
}

// Forget drops the observations of a removed source.
func (o *Observations) Forget(sourceID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.observed, sourceID)
	delete(o.withheld, sourceID)
}

// Scoped returns a frozen copy holding only the given sources, for a reader
// snapshot restricted to them.
func (o *Observations) Scoped(sources []string) *Observations {
	out := &Observations{observed: map[string]time.Time{}, withheld: map[string]int{}}
	if o == nil {
		return out
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, id := range sources {
		if t, ok := o.observed[id]; ok {
			out.observed[id] = t
		}
		if n, ok := o.withheld[id]; ok {
			out.withheld[id] = n
		}
	}
	return out
}

// MarkObserved records that sourceID was observed now. A failed import must
// never advance this clock. The caller holds the store lock.
func (s Service) MarkObserved(sourceID string) {
	s.Observations.MarkObserved(sourceID, time.Now().UTC())
}

// MarkWithheld records the withheld count of a successful import of sourceID.
// The caller holds the store lock.
func (s Service) MarkWithheld(sourceID string, count int) {
	s.Observations.MarkWithheld(sourceID, count)
}
