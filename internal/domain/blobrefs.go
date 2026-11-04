package domain

// ReferencedBlobs returns the digest of every image blob the database
// references: source images (including the metadata image, which is one of
// them), the images of every stored and published revision, and those of
// pending outbox jobs and moves. Blob collection deletes only blobs outside
// this set, so every persisted BlobRef field must be read here.
func ReferencedBlobs(db Database) map[string]bool {
	refs := map[string]bool{}
	add := func(assets map[string]BlobRef) {
		for _, ref := range assets {
			refs[ref.SHA] = true
		}
	}
	for _, source := range db.Sources {
		add(source.Assets)
	}
	for _, article := range db.Articles {
		for _, revision := range article.Versions {
			add(revision.Assets)
		}
		if article.Published != nil {
			add(article.Published.Assets)
		}
	}
	for _, job := range db.Outbox {
		add(job.Assets)
	}
	for _, move := range db.Moves {
		add(move.Assets)
	}
	return refs
}
