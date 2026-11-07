package httpapi

import (
	"time"

	"github.com/Mesteriis/veda-wiki/internal/agentctx"
	"github.com/Mesteriis/veda-wiki/internal/auth"
	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/importer"
	"github.com/Mesteriis/veda-wiki/internal/search"
	"github.com/Mesteriis/veda-wiki/internal/store"
	"github.com/Mesteriis/veda-wiki/internal/syncer"
	"github.com/Mesteriis/veda-wiki/internal/transfer"
)

// dataStore is the data one request may reach: the shared store, or for a
// restricted reader the snapshot of its granted sources, with the matching
// import observations. Area services are bound to it per request, so a
// restricted reader's operations only ever see its snapshot.
type dataStore struct {
	*store.Store
	observations *importer.Observations
	bodies       *search.Cache // lowercased revision bodies for search, shared by scoped copies
}

// scoped wraps a reader snapshot with a frozen copy of the import
// observations of the sources it grants. The copy is taken just after the
// snapshot, under the observations' own lock, not the snapshot's store lock.
func (s *dataStore) scoped(view *store.Store) *dataStore {
	var granted []string
	_ = view.View(func(db *domain.Database) error {
		for id := range db.Sources {
			granted = append(granted, id)
		}
		return nil
	})
	return &dataStore{Store: view, observations: s.observations.Scoped(granted), bodies: s.bodies}
}

func (s *dataStore) auth() auth.Service { return auth.Service{Store: s.Store} }

func (s *dataStore) importer() importer.Service {
	return importer.Service{Store: s.Store, Observations: s.observations}
}

// transfer reports source statuses as the agent context does.
func (s *dataStore) transfer() transfer.Service {
	return transfer.Service{Store: s.Store, Status: s.sourceStatus}
}

func (s *dataStore) sourceStatus(db domain.Database, sourceID string, src domain.Source) domain.SourceStatus {
	return agentctx.Status(db, s.observations, sourceID, src, time.Now())
}

// syncer returns a synchronization manager without a GitHub client, for
// requests that never reach GitHub.
func (s *dataStore) syncer() *syncer.Manager {
	imp := s.importer()
	return syncer.New(s.Store, nil, &imp)
}

func (s *dataStore) addSource(src domain.Source) error {
	if !domain.ValidNewSource(src) {
		return domain.ErrInvalid
	}
	return s.Update(func(tx *store.Tx) error { return domain.AddSource(tx.DB(), src) })
}

// put writes one article revision. Only that article's records are persisted
// unless completing a pending move can change others.
func (s *dataStore) put(sourceID, p, text string, expected int) (domain.Revision, error) {
	return s.putVia(sourceID, p, text, expected, "")
}

// putVia is put that records the client that wrote the revision.
func (s *dataStore) putVia(sourceID, p, text string, expected int, via string) (domain.Revision, error) {
	if !domain.ValidArticleText(text, expected) {
		return domain.Revision{}, domain.ErrInvalid
	}
	var rev domain.Revision
	err := s.Update(func(tx *store.Tx) error {
		written, key, err := domain.PutArticleVia(tx.DB(), sourceID, p, text, expected, via, time.Now())
		if err != nil {
			return err
		}
		if key != "" {
			tx.OnlyArticle(key)
		}
		rev = written
		return nil
	})
	if err != nil {
		return domain.Revision{}, err
	}
	return rev, nil
}

// editVia writes a revision that replaces one unique fragment of the
// current revision, under the same lock as the read of that revision.
func (s *dataStore) editVia(sourceID, p, old, replacement string, expected int, via string) (domain.Revision, error) {
	var rev domain.Revision
	err := s.Update(func(tx *store.Tx) error {
		written, key, err := domain.EditArticleVia(tx.DB(), sourceID, p, old, replacement, expected, via, time.Now())
		if err != nil {
			return err
		}
		if key != "" {
			tx.OnlyArticle(key)
		}
		rev = written
		return nil
	})
	return rev, err
}

// sources adds the import observations to the readable sources; only
// non-public readers receive them.
func (s *dataStore) sources(public bool) []domain.SourceView {
	var out []domain.SourceView
	_ = s.View(func(db *domain.Database) error {
		out = domain.ReadSources(*db, public, time.Now())
		if public {
			return nil
		}
		for i := range out {
			if observed, ok := s.observations.Observed(out[i].ID); ok {
				out[i].LastImportObservedAt = observed.UTC().Format(time.RFC3339Nano)
			}
			if count, ok := s.observations.Withheld(out[i].ID); ok {
				out[i].LastImportWithheld = &count
			}
		}
		return nil
	})
	return out
}

func (s *dataStore) snapshot(public bool) domain.Snapshot {
	var snap domain.Snapshot
	_ = s.View(func(db *domain.Database) error {
		snap = domain.ReadSnapshot(*db, public, time.Now())
		return nil
	})
	return snap
}

func (s *dataStore) page(key string, public bool) (domain.Page, error) {
	var page domain.Page
	err := s.View(func(db *domain.Database) error {
		var err error
		page, err = domain.ReadPage(*db, key, public, time.Now())
		return err
	})
	return page, err
}

func (s *dataStore) article(sourceID, p string, public bool) (domain.Article, error) {
	var a domain.Article
	err := s.View(func(db *domain.Database) error {
		var err error
		a, err = domain.ReadArticle(*db, sourceID, p, public, time.Now())
		return err
	})
	return a, err
}

// findArticles ranks against candidates taken under the store read lock;
// ranking runs after the lock is released.
func (s *dataStore) findArticles(query, sourceID string, public bool) []search.Hit {
	var candidates []search.Candidate
	_ = s.View(func(db *domain.Database) error {
		candidates = search.Candidates(*db, sourceID, public, time.Now())
		return nil
	})
	return search.Rank(candidates, query, s.bodies)
}

// findPage is findArticles for one page of results, each with its best
// matching line, and the total number of matches.
func (s *dataStore) findPage(query, sourceID string, public bool, offset, limit int) ([]search.Hit, int) {
	var candidates []search.Candidate
	_ = s.View(func(db *domain.Database) error {
		candidates = search.Candidates(*db, sourceID, public, time.Now())
		return nil
	})
	return search.Page(candidates, query, offset, limit, s.bodies)
}
