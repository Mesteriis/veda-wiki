package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
)

// Read errors. Callers map them to fixed codes and never echo them.
var (
	// ErrInvalidRequest: a read parameter is malformed or out of bounds.
	ErrInvalidRequest = errors.New("knowledge: invalid request")
	// ErrReadUnavailable: the index cannot answer now, or the pipeline
	// (extractor settings) is not known yet.
	ErrReadUnavailable = errors.New("knowledge: index unavailable")
)

// Read bounds of the read service, shared by REST and MCP.
const (
	// MaxPageLimit bounds a page of entities and the lists of an entity
	// card or an article view.
	MaxPageLimit = 100
	// MaxOffset bounds entity paging.
	MaxOffset = 10000
	// MaxQueryRunes bounds a search or context query text.
	MaxQueryRunes = 200
	// MinContextLimitBytes..MaxContextLimitBytes bound the exact JSON size
	// of the selected items array ("[]" is 2 bytes);
	// DefaultContextLimitBytes applies when the caller sets none.
	MinContextLimitBytes     = 2
	MaxContextLimitBytes     = 16000
	DefaultContextLimitBytes = 8000
	// MaxContextItems bounds the selected context items.
	MaxContextItems = 50
	// MaxContextSeeds is the number of lexical search hits used as seeds.
	MaxContextSeeds = 5
	// MaxGraphFilters bounds the source and relation filters of a graph.
	MaxGraphFilters = 20
	// DefaultReadTimeout bounds one index query (Config.ReadTimeout).
	DefaultReadTimeout = 10 * time.Second
)

var entityIDPattern = regexp.MustCompile(`^ent-[0-9a-f]{20}$`)

// ReadScope is the access of one read request: the request-scoped store
// (the full store, or the snapshot of a scoped key, scoped session or team
// user) and the computed mode, Public = !owner && !reader. Every read builds
// its allowed set from it on the call, so expired keys, revoked grants,
// visibility changes, partial imports and expired visibility proofs close
// access at once, without waiting for the index.
type ReadScope struct {
	Store  Snapshots
	Public bool
	Owner  bool
	// Now is the clock of the access rules (default time.Now).
	Now time.Time
}

// ArticleRef names an article: a stable source ID and a path.
type ArticleRef struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
}

// readAccess is what one read may see: the allowed set and, per readable
// article, its visible snapshot (public: the published revision; reader
// and owner: the current revision).
type readAccess struct {
	allowed AllowedSet
	visible map[ArticleRef]SnapshotKey
	db      *domain.Database
}

func (rs ReadScope) now() time.Time {
	if rs.Now.IsZero() {
		return time.Now()
	}
	return rs.Now
}

// access builds the allowed set under the store's read lock. The visible
// snapshot of each article is the one domain.ReadSnapshot selects; the
// owner's publication copy is added only by withCopy (article reads and
// article-centred graphs that name its SHA).
func (rs ReadScope) access(within func(a readAccess) error) error {
	if rs.Store == nil {
		return ErrReadUnavailable
	}
	return rs.Store.View(func(db *domain.Database) error {
		a := readAccess{allowed: Allowed(*db, rs.Public, Scope{}, rs.now()), visible: map[ArticleRef]SnapshotKey{}, db: db}
		for k := range a.allowed.keys {
			a.visible[ArticleRef{SourceID: k.SourceID, Path: k.Path}] = k
		}
		return within(a)
	})
}

// pick resolves the snapshot of ref the request reads: the visible one, or
// with sha the visible one or, for the owner only, the publication copy.
// The returned set is the one to query with. Not readable: ErrNotFound.
func (rs ReadScope) pick(ref ArticleRef, sha string) (SnapshotKey, AllowedSet, error) {
	var key SnapshotKey
	var set AllowedSet
	err := rs.access(func(a readAccess) error {
		k, ok := a.visible[ref]
		if !ok {
			return ErrNotFound
		}
		key, set = k, a.allowed
		if sha == "" || sha == k.SHA {
			return nil
		}
		if rs.Public || !rs.Owner {
			return ErrNotFound
		}
		withCopy := Allowed(*a.db, false, Scope{Owner: true}, rs.now())
		copyKey := SnapshotKey{SourceID: ref.SourceID, Path: ref.Path, SHA: sha}
		if !withCopy.Has(copyKey) {
			return ErrNotFound
		}
		// The owner's current revisions with this one article swapped for
		// its publication copy: no other copy, no article twice.
		key, set = copyKey, a.allowed.replace(k, copyKey)
		return nil
	})
	return key, set, err
}

func (rs ReadScope) allowed() (AllowedSet, error) {
	var set AllowedSet
	err := rs.access(func(a readAccess) error { set = a.allowed; return nil })
	return set, err
}

// ReadTimeout is the deadline of one index query.
func (s *Service) ReadTimeout() time.Duration { return s.cfg.ReadTimeout }

// readContext applies the read timeout and the active fingerprint.
func (s *Service) readContext(ctx context.Context) (context.Context, context.CancelFunc, Fingerprint, error) {
	p, ok := s.Pipeline()
	if !ok {
		return ctx, func() {}, "", ErrReadUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.ReadTimeout)
	return ctx, cancel, p.Fingerprint, nil
}

// indexError keeps not found and deadlines and hides every other index
// error behind ErrReadUnavailable.
func indexError(ctx context.Context, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return ErrNotFound
	case ctx.Err() != nil:
		return context.DeadlineExceeded
	}
	return ErrReadUnavailable
}

func validText(text string, required bool) bool {
	n := utf8.RuneCountInString(text)
	return utf8.ValidString(text) && n <= MaxQueryRunes && (!required || n > 0)
}

func validLimit(limit int) bool { return limit >= 0 && limit <= MaxPageLimit }

func validRef(ref ArticleRef) bool {
	return domain.ValidID(ref.SourceID) && domain.SafeArticlePath(ref.Path)
}

func validSHA(sha string) bool {
	if sha == "" {
		return true
	}
	_, err := domain.HexKey(sha)
	return len(sha) == 64 && err == nil
}

// Entities searches the entities the request may read. Names, aliases,
// counts and ranking come only from allowed evidence.
func (s *Service) Entities(ctx context.Context, rs ReadScope, q EntityQuery) (EntityPage, error) {
	if !validText(q.Text, false) || !validLimit(q.Limit) || q.Offset < 0 || q.Offset > MaxOffset ||
		(q.SourceID != "" && !domain.ValidID(q.SourceID)) || (q.Type != "" && !s.cfg.Schema.HasEntity(q.Type)) {
		return EntityPage{}, ErrInvalidRequest
	}
	if q.Limit == 0 {
		q.Limit = DefaultLimit
	}
	allowed, err := rs.allowed()
	if err != nil {
		return EntityPage{}, err
	}
	ctx, cancel, fp, err := s.readContext(ctx)
	defer cancel()
	if err != nil {
		return EntityPage{}, err
	}
	page, err := s.cfg.Index.Entities(ctx, allowed, fp, q)
	return page, indexError(ctx, err)
}

// Entity is one entity with its allowed evidence. An unknown entity and one
// only hidden evidence mentions are the same ErrNotFound.
func (s *Service) Entity(ctx context.Context, rs ReadScope, id string, limit int) (EntityCard, error) {
	if !entityIDPattern.MatchString(id) || !validLimit(limit) {
		return EntityCard{}, ErrInvalidRequest
	}
	allowed, err := rs.allowed()
	if err != nil {
		return EntityCard{}, err
	}
	ctx, cancel, fp, err := s.readContext(ctx)
	defer cancel()
	if err != nil {
		return EntityCard{}, err
	}
	card, err := s.cfg.Index.Entity(ctx, allowed, fp, id, limit)
	return card, indexError(ctx, err)
}

// GraphRequest asks for a bounded subgraph around an entity or an article.
// SHA, for an article centre, selects the owner's publication copy.
type GraphRequest struct {
	EntityID  string
	Article   *ArticleRef
	SHA       string
	Depth     int
	SourceIDs []string
	Relations []string
	MaxNodes  int
	MaxEdges  int
}

// Graph is a subgraph of allowed evidence only: traversal never passes a
// hidden node, and Truncated reports a hit cap.
func (s *Service) Graph(ctx context.Context, rs ReadScope, q GraphRequest) (Subgraph, error) {
	limits := s.cfg.Schema.Limits
	if (q.EntityID == "") == (q.Article == nil) || q.Depth < 0 || q.Depth > limits.DepthMax ||
		q.MaxNodes < 0 || q.MaxNodes > limits.NodesMax || q.MaxEdges < 0 || q.MaxEdges > limits.EdgesMax ||
		len(q.SourceIDs) > MaxGraphFilters || len(q.Relations) > MaxGraphFilters || !validSHA(q.SHA) || (q.SHA != "" && q.Article == nil) {
		return Subgraph{}, ErrInvalidRequest
	}
	if q.EntityID != "" && !entityIDPattern.MatchString(q.EntityID) || q.Article != nil && !validRef(*q.Article) {
		return Subgraph{}, ErrInvalidRequest
	}
	for _, id := range q.SourceIDs {
		if !domain.ValidID(id) {
			return Subgraph{}, ErrInvalidRequest
		}
	}
	for _, r := range q.Relations {
		if !s.cfg.Schema.hasRelation(r) {
			return Subgraph{}, ErrInvalidRequest
		}
	}
	gq := GraphQuery{Center: GraphCenter{EntityID: q.EntityID}, Depth: q.Depth, SourceIDs: q.SourceIDs, Relations: q.Relations, MaxNodes: q.MaxNodes, MaxEdges: q.MaxEdges}
	var allowed AllowedSet
	var err error
	if q.Article != nil {
		var key SnapshotKey
		key, allowed, err = rs.pick(*q.Article, q.SHA)
		gq.Center.Snapshot = &key
	} else {
		allowed, err = rs.allowed()
	}
	if err != nil {
		return Subgraph{}, err
	}
	ctx, cancel, fp, err := s.readContext(ctx)
	defer cancel()
	if err != nil {
		return Subgraph{}, err
	}
	g, err := s.cfg.Index.Graph(ctx, allowed, fp, gq)
	return g, indexError(ctx, err)
}

// ContextRequest asks for graph context. Seeds are VEDA's lexical search
// hits over the same request scope, best first; the read service maps them
// to the visible snapshots and drops any it may not read.
type ContextRequest struct {
	Text       string
	Seeds      []ArticleRef
	Depth      int
	LimitBytes int
}

// ContextAnswer is the selected evidence with the reasons and the exact
// size cap that applied. Nothing in it is generated.
type ContextAnswer struct {
	ContextResult
	Query      string `json:"query"`
	Depth      int    `json:"depth"`
	LimitBytes int    `json:"limit_bytes"`
}

// Context selects evidence for a query: entities named in the text, the
// lexical hits and their entities, and claims reached within Depth, until
// LimitBytes of JSON items.
func (s *Service) Context(ctx context.Context, rs ReadScope, q ContextRequest) (ContextAnswer, error) {
	limits := s.cfg.Schema.Limits
	if !validText(q.Text, true) || q.Depth < 0 || q.Depth > limits.DepthMax || (q.LimitBytes != 0 && q.LimitBytes < MinContextLimitBytes) || q.LimitBytes < 0 || q.LimitBytes > MaxContextLimitBytes {
		return ContextAnswer{}, ErrInvalidRequest
	}
	if q.Depth == 0 {
		q.Depth = limits.DepthDefault
	}
	if q.LimitBytes == 0 {
		q.LimitBytes = DefaultContextLimitBytes
	}
	var allowed AllowedSet
	var seeds []SnapshotKey
	err := rs.access(func(a readAccess) error {
		allowed = a.allowed
		for _, ref := range q.Seeds {
			if k, ok := a.visible[ref]; ok && len(seeds) < MaxContextSeeds {
				seeds = append(seeds, k)
			}
		}
		return nil
	})
	if err != nil {
		return ContextAnswer{}, err
	}
	ctx, cancel, fp, err := s.readContext(ctx)
	defer cancel()
	if err != nil {
		return ContextAnswer{}, err
	}
	res, err := s.cfg.Index.Context(ctx, allowed, fp, ContextQuery{Text: q.Text, Seeds: seeds, Depth: q.Depth, LimitBytes: q.LimitBytes, MaxItems: MaxContextItems})
	if err = indexError(ctx, err); err != nil {
		return ContextAnswer{}, err
	}
	if res, err = fitContext(res, q.LimitBytes); err != nil {
		return ContextAnswer{}, ErrReadUnavailable
	}
	return ContextAnswer{ContextResult: res, Query: q.Text, Depth: q.Depth, LimitBytes: q.LimitBytes}, nil
}

// fitContext holds every index to the same contract: Bytes is the exact
// size of the JSON items array, and items are dropped from the end until it
// is at most limit (Truncated then).
func fitContext(res ContextResult, limit int) (ContextResult, error) {
	if res.Items == nil {
		res.Items = []ContextItem{}
	}
	for {
		data, err := json.Marshal(res.Items)
		if err != nil {
			return ContextResult{}, err
		}
		if len(data) <= limit {
			res.Bytes = len(data)
			return res, nil
		}
		res.Items, res.Truncated = res.Items[:len(res.Items)-1], true
	}
}
