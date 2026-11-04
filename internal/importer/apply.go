package importer

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mesteriis/veda-wiki/internal/domain"
	"github.com/Mesteriis/veda-wiki/internal/store"
)

// errUnchanged ends the transaction of an import whose generation is already
// current without committing anything.
var errUnchanged = errors.New("import unchanged")

// Import applies a batch from an owner request: no remote proof and no
// restriction to an existing local source.
func (s Service) Import(batch Batch) (Result, error) {
	return s.Apply(batch, nil, false)
}

// Apply validates batch and applies it in one store transaction. A batch
// without proof is a local checkout observation; with proof it is a verified
// GitHub pull. localRoot restricts the batch to updating a pre-existing,
// disconnected private source.
//
// Images are written and synced inside the transaction, before the commit
// that references them, so a failed commit may leave an unreferenced blob but
// never a referenced partial file. The observation of a successful import is
// recorded under the same write lock, after its commit.
func (s Service) Apply(batch Batch, proof *RemoteProof, localRoot bool) (Result, error) {
	result := Result{Withheld: batch.Withheld}
	if !domain.ValidID(batch.SourceID) ||
		(batch.Kind != "wiki" && batch.Kind != "program") ||
		!domain.ValidCommitID(batch.Commit) ||
		batch.ExpectedImport == nil ||
		batch.Withheld < 0 ||
		len(batch.Files)+len(batch.Assets) > 2000 {
		return result, domain.ErrInvalid
	}
	// A proof without its hook would apply the pull without recording it.
	if proof != nil && (proof.Record == nil || proof.Adoption != nil && proof.Adoption.Attach == nil) {
		return result, domain.ErrInvalid
	}
	if batch.Kind == "wiki" {
		if e := domain.ValidateMetadata(batch.Metadata); e != nil {
			return result, e
		}
	} else if batch.Metadata != nil {
		return result, domain.ErrInvalid
	}
	total := 0
	for p, text := range batch.Files {
		if !domain.ArticlePath(p, batch.Kind) || len(text) > domain.MaxArticleBytes || !utf8.ValidString(text) || domain.CredentialLike(text) {
			return result, domain.ErrInvalid
		}
		total += len(text)
	}
	refs := map[string]domain.BlobRef{}
	for name, data := range batch.Assets {
		if !domain.SafeRelative(name) || (batch.Kind == "program" && !strings.HasPrefix(name, "docs/")) {
			return result, domain.ErrInvalid
		}
		ref, e := domain.ImageRef(data)
		if e != nil {
			return result, e
		}
		refs[name] = ref
		total += len(data)
	}
	if total > MaxBytes {
		return result, domain.ErrInvalid
	}
	if batch.Metadata != nil && batch.Metadata.Image != "" {
		if _, ok := refs[batch.Metadata.Image]; !ok {
			return result, domain.ErrInvalid
		}
	}
	canonical, _ := json.Marshal(struct {
		Files    map[string]string
		Assets   map[string]domain.BlobRef
		Commit   string
		Metadata *domain.WikiMetadata
		Dirty    bool
		Partial  bool
	}{batch.Files, refs, batch.Commit, batch.Metadata, batch.Dirty, !batch.Complete || batch.Withheld > 0})
	generation := domain.Digest(canonical)
	var applyErr error
	err := s.Store.Update(func(tx *store.Tx) error {
		var unchanged bool
		result, unchanged, applyErr = s.apply(tx, batch, proof, localRoot, refs, generation, result)
		if applyErr != nil {
			return applyErr
		}
		if unchanged {
			// Nothing to commit: the observation is final under this lock.
			s.markImport(batch)
			return errUnchanged
		}
		tx.AfterCommit(func() { s.markImport(batch) })
		return nil
	})
	switch {
	case applyErr != nil:
		return result, applyErr
	case errors.Is(err, errUnchanged):
		return result, nil
	case err != nil:
		return Result{}, err
	}
	return result, nil
}

// markImport records a successful import. The caller holds the store lock;
// failed imports must never advance the observation clock.
func (s Service) markImport(batch Batch) {
	s.MarkObserved(batch.SourceID)
	s.MarkWithheld(batch.SourceID, batch.Withheld)
}

// storeAssets writes verified immutable blobs before the commit that
// references them. A failed commit may leave an unreferenced blob, but never
// a partial file at a referenced digest. The caller holds the store lock.
func (s Service) storeAssets(refs map[string]domain.BlobRef, assets map[string][]byte) error {
	blobs := make(map[string][]byte, len(refs))
	for name := range refs {
		blobs[name] = assets[name]
	}
	stored, err := s.Store.WriteBlobs(blobs)
	if err != nil {
		return err
	}
	for name, ref := range refs {
		if stored[name] != ref {
			return domain.ErrInvalid
		}
	}
	return nil
}

// assetsAvailable reports whether every referenced blob is stored intact.
func (s Service) assetsAvailable(assets map[string]domain.BlobRef) bool {
	return s.Store.VerifyBlobs(assets) == nil
}

// apply runs inside the import transaction. It reports unchanged when the
// source already holds this generation and nothing must be committed.
func (s Service) apply(tx *store.Tx, batch Batch, proof *RemoteProof, localRoot bool, refs map[string]domain.BlobRef, generation string, result Result) (Result, bool, error) {
	db := tx.DB()
	src, exists := db.Sources[batch.SourceID]
	// The privileged local ingest route can update only a pre-existing,
	// disconnected private source. Check this inside the import transaction.
	if localRoot && (!exists || src.Visibility != "private" || src.ConnectionID != "" || batch.Create) {
		return result, false, domain.ErrInvalid
	}
	if !exists {
		if !batch.Create || (batch.Visibility != "public" && batch.Visibility != "private") {
			return result, false, domain.ErrNotFound
		}
		src = domain.Source{ID: batch.SourceID, Title: batch.SourceID, Kind: batch.Kind, Visibility: batch.Visibility}
	} else if batch.Create {
		return result, false, domain.ErrConflict
	}
	wasPrivate := src.Visibility != "public"
	if proof == nil && src.ConnectionID != "" {
		return result, false, domain.ErrInvalid
	}
	if proof != nil {
		if proof.Adoption != nil {
			adopt := proof.Adoption
			conn := adopt.Connection
			current, err := domain.AdoptionSnapshot(*db, batch.SourceID)
			if tx.StoreInvalid() {
				err = domain.ErrConflict
			}
			account, accountOK := db.Accounts[conn.AccountID]
			if err != nil ||
				!exists ||
				current != adopt.Snapshot ||
				!accountOK ||
				account != adopt.Account ||
				conn.AccountID == "" ||
				conn.ID != batch.SourceID ||
				conn.ID != proof.ConnectionID ||
				conn.ConfigRevision != proof.ConfigRevision ||
				!conn.Private ||
				proof.Visibility != "private" ||
				!batch.Complete ||
				batch.Withheld != 0 {
				return result, false, domain.ErrConflict
			}
			for name, old := range src.Assets {
				if incoming, ok := refs[name]; !ok || incoming != old {
					return result, false, domain.ErrConflict
				}
			}
			src.ConnectionID = conn.ID
		} else {
			conn, ok := db.Connections[proof.ConnectionID]
			if !ok || conn.Paused || conn.ConfigRevision != proof.ConfigRevision || conn.ID != batch.SourceID || (exists && src.ConnectionID != conn.ID) {
				return result, false, domain.ErrConflict
			}
			src.ConnectionID = conn.ID
		}
		src.Visibility = proof.Visibility
	}
	if src.Kind != batch.Kind || src.ImportGeneration != *batch.ExpectedImport {
		return result, false, domain.ErrConflict
	}
	if e := s.storeAssets(refs, batch.Assets); e != nil {
		return result, false, e
	}
	if proof == nil && exists && src.ImportGeneration == generation {
		for _, ref := range src.Assets {
			if s.Store.VerifyBlob(ref) != nil {
				return Result{}, false, ErrAssetUnavailable
			}
		}
		result.Generation = generation
		result.Unchanged = len(batch.Files)
		return result, true, nil
	}
	// Publication snapshots as committed before this transaction changed them.
	previousPublished := make(map[string]*domain.Revision, len(batch.Files))
	for p := range batch.Files {
		previousPublished[p] = db.Articles[batch.SourceID+"/"+p].Published
	}
	if proof != nil && proof.Adoption != nil {
		proof.Adoption.Attach(db)
	}
	if proof != nil && proof.Visibility == "public" {
		for key, a := range db.Articles {
			if a.SourceID == batch.SourceID && (wasPrivate || !a.PublishedLocal) {
				a.Published = nil
				a.PublishedLocal = false
				db.Articles[key] = a
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	paths := make([]string, 0, len(batch.Files))
	for p := range batch.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		text := batch.Files[p]
		hash := domain.Digest([]byte(text))
		key := batch.SourceID + "/" + p
		a, found := db.Articles[key]
		if proof != nil && proof.Visibility == "public" && !a.PublishedLocal {
			published := domain.Revision{Text: text, SHA: hash, Created: now}
			if previous := previousPublished[p]; previous != nil && previous.SHA == hash {
				published = *previous
			}
			a.Published = &published
			db.Articles[key] = a
		}
		if proof != nil && domain.ObservedWrite(db, batch.SourceID, p, hash, batch.Commit, refs, batch.Files, batch.Complete && batch.Withheld == 0) {
			a.UpstreamHash = hash
			a = supersedeImportConflicts(a)
			db.Articles[key] = a
			result.Unchanged++
			continue
		}
		if found && a.UpstreamHash == hash && !a.Deleted {
			db.Articles[key] = supersedeImportConflicts(a)
			result.Unchanged++
			continue
		}
		latest := ""
		if found {
			last, _ := domain.LastRevision(a)
			latest = last.SHA
		}
		if found && !a.Deleted && latest != a.UpstreamHash && (latest != hash || (proof != nil && domain.PreparedWriteDiffers(*db, batch.SourceID, p, hash))) {
			if hasImportConflict(a, hash, false) {
				result.Conflicts++
				continue
			}
			a = supersedeImportConflicts(a)
			a.Conflicts = append(append([]domain.ImportConflict(nil), a.Conflicts...), domain.ImportConflict{Incoming: text, IncomingSHA: hash, BaseSHA: a.UpstreamHash, Commit: batch.Commit, Created: now})
			db.Articles[key] = a
			result.Conflicts++
			continue
		}
		if found && latest == hash && !a.Deleted {
			a = supersedeImportConflicts(a)
			a.UpstreamHash = hash
			db.Articles[key] = a
			result.Unchanged++
			continue
		}
		a = supersedeImportConflicts(a)
		a.SourceID, a.Path, a.Deleted, a.UpstreamHash = batch.SourceID, p, false, hash
		a.Versions = append(append([]domain.Revision(nil), a.Versions...), domain.Revision{Number: domain.LatestRevision(a) + 1, Text: text, SHA: hash, Created: now})
		db.Articles[key] = a
		if found {
			result.Updated++
		} else {
			result.Created++
		}
	}
	if batch.Complete && batch.Withheld == 0 {
		applyDeletions(db, batch, proof, now, &result)
	}
	if exists && wasPrivate && src.Visibility == "private" {
		retainPrivateAssets(db, batch, src.Assets, refs)
	}
	// Retained images may not be present in the new batch and therefore cannot
	// be repaired from it. Do not commit or report a successful observation if
	// any image that would remain referenced is missing or corrupt.
	for _, ref := range refs {
		if s.Store.VerifyBlob(ref) != nil {
			return Result{}, false, ErrAssetUnavailable
		}
	}
	bindImportedAssets(db, batch, refs, proof != nil && proof.Visibility == "public")
	src.Metadata = batch.Metadata
	src.Assets = refs
	src.ImportCommit = batch.Commit
	src.ImportDirty = batch.Dirty
	src.ImportPartial = !batch.Complete || batch.Withheld > 0
	if batch.Metadata != nil {
		src.Title = batch.Metadata.Title
	}
	src.ImportGeneration = generation
	result.Generation = src.ImportGeneration
	db.Sources[src.ID] = src
	if proof != nil && batch.Complete && batch.Withheld == 0 {
		domain.ConfirmSourceDeletions(db, proof.ConnectionID, batch.Files, s.assetsAvailable)
	}
	domain.ReconcileMoves(db)
	if proof != nil {
		proof.Record(db)
	}
	if proof != nil && proof.Adoption != nil {
		if err := proof.Adoption.Context.Err(); err != nil {
			return Result{}, false, err
		}
	}
	return result, false, nil
}

// applyDeletions handles articles a complete observation no longer contains:
// unchanged ones become tombstones, locally changed ones get a conflict.
func applyDeletions(db *domain.Database, batch Batch, proof *RemoteProof, now string, result *Result) {
	for key, a := range db.Articles {
		if a.SourceID != batch.SourceID || a.Deleted || a.UpstreamHash == "" {
			continue
		}
		if _, present := batch.Files[a.Path]; present {
			continue
		}
		if proof != nil && proof.Visibility == "public" && !a.PublishedLocal {
			a.Published = nil
			db.Articles[key] = a
		}
		last, _ := domain.LastRevision(a)
		latest := last.SHA
		if latest != a.UpstreamHash {
			if hasImportConflict(a, "", true) {
				result.Conflicts++
				continue
			}
			a = supersedeImportConflicts(a)
			a.Conflicts = append(append([]domain.ImportConflict(nil), a.Conflicts...), domain.ImportConflict{BaseSHA: a.UpstreamHash, Commit: batch.Commit, Deleted: true, Created: now})
			result.Conflicts++
		} else {
			a = supersedeImportConflicts(a)
			a.Deleted = true
			result.Deleted++
		}
		db.Articles[key] = a
	}
}

// retainPrivateAssets adds to refs the images a private source keeps.
// Partial observations cannot prove that an old image was removed. A locally
// changed article also keeps its missing referenced images while an upstream
// deletion remains unresolved. Never carry private assets into a newly public
// source; publication needs a fresh, verified snapshot.
func retainPrivateAssets(db *domain.Database, batch Batch, previous, refs map[string]domain.BlobRef) {
	if !batch.Complete || batch.Withheld > 0 {
		for name, ref := range previous {
			if _, present := refs[name]; !present {
				refs[name] = ref
			}
		}
	}
	for _, article := range db.Articles {
		if article.SourceID != batch.SourceID || article.Deleted || domain.LatestRevision(article) == 0 {
			continue
		}
		latest, _ := domain.LastRevision(article)
		if article.UpstreamHash != "" && latest.SHA == article.UpstreamHash {
			continue
		}
		retained := domain.RetainedPendingAssets(latest.Text, article.Path, previous)
		if len(retained) > 0 && domain.HasRootReference(latest.Text) && db.Version < 12 {
			db.Version = 12
		}
		for name, ref := range retained {
			if _, present := refs[name]; !present {
				refs[name] = ref
			}
		}
	}
}

// bindImportedAssets gives every revision this import created or observed
// unchanged, and every publication copy it made, the images the imported
// text references in the final index refs. A revision whose text equals the
// imported file mirrors the observed commit, so it follows that commit's
// images even when only an image changed. Locally changed revisions and
// local publications keep the images they were written with.
func bindImportedAssets(db *domain.Database, batch Batch, refs map[string]domain.BlobRef, publicProof bool) {
	index := domain.Source{Assets: refs}
	for p, text := range batch.Files {
		key := batch.SourceID + "/" + p
		a, found := db.Articles[key]
		if !found || a.Deleted {
			continue
		}
		hash := domain.Digest([]byte(text))
		assets := domain.RevisionAssets(text, p, index)
		if n := len(a.Versions); n > 0 && a.Versions[n-1].SHA == hash {
			a.Versions = append([]domain.Revision(nil), a.Versions...)
			a.Versions[n-1].Assets = assets
		}
		if publicProof && !a.PublishedLocal && a.Published != nil && a.Published.SHA == hash {
			published := *a.Published
			published.Assets = assets
			a.Published = &published
		}
		db.Articles[key] = a
	}
}
