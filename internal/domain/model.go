package domain

import (
	"html/template"
)

// Source is one wiki or program documentation source and the state of its
// last import.
type Source struct {
	ID               string             `json:"id"`
	Title            string             `json:"title"`
	Kind             string             `json:"kind"`
	Visibility       string             `json:"visibility"`
	Metadata         *WikiMetadata      `json:"metadata,omitempty"`
	Assets           map[string]BlobRef `json:"assets,omitempty"`
	ImportGeneration string             `json:"import_generation,omitempty"`
	ImportCommit     string             `json:"import_commit,omitempty"`
	ImportDirty      bool               `json:"import_dirty,omitempty"`
	ImportPartial    bool               `json:"import_partial,omitempty"`
	ConnectionID     string             `json:"connection_id,omitempty"`
}

// SourceView adds ephemeral, owner-visible sync evidence without persisting a
// timestamp on every unchanged snapshot import.
type SourceView struct {
	Source
	LastImportObservedAt string `json:"last_import_observed_at,omitempty"`
	LastImportWithheld   *int   `json:"last_import_withheld,omitempty"`
	UnresolvedConflicts  int    `json:"unresolved_conflicts,omitempty"`
	SyncIssue            string `json:"-"`
}

// Revision is one immutable version of an article's text.
type Revision struct {
	Number  int    `json:"revision"`
	Text    string `json:"text"`
	SHA     string `json:"sha256"`
	Created string `json:"created_at"`
	// Assets are the images the text referenced when the revision was
	// written, by repository path. Revisions written before images were
	// bound have none and display the source's current images.
	Assets map[string]BlobRef `json:"assets,omitempty"`
	// Via records the client that wrote the revision through the API
	// (one of RevisionVia); imported revisions and older ones leave it empty.
	Via string `json:"via,omitempty"`
}

// RevisionVia lists the recorded write origins: MCP in editor or owner mode,
// or another REST client.
var RevisionVia = map[string]bool{"mcp-editor": true, "mcp-owner": true, "rest": true}

// Article is one Markdown file of a source with its revision history,
// import conflicts and publication state.
type Article struct {
	CopiedFrom     *CopyOrigin      `json:"copied_from,omitempty"`
	ReconcileBase  *ReconcileBase   `json:"reconcile_base,omitempty"`
	SourceID       string           `json:"source_id"`
	Path           string           `json:"path"`
	Versions       []Revision       `json:"versions"`
	UpstreamHash   string           `json:"upstream_sha256,omitempty"`
	Deleted        bool             `json:"deleted,omitempty"`
	Conflicts      []ImportConflict `json:"conflicts,omitempty"`
	Published      *Revision        `json:"published,omitempty"`
	PublishedLocal bool             `json:"published_local,omitempty"`
}

// Database is the whole persisted state of the data directory.
type Database struct {
	Version       int                         `json:"version"`
	Updated       string                      `json:"updated_at"`
	Sources       map[string]Source           `json:"sources"`
	Articles      map[string]Article          `json:"articles"`
	Accounts      map[string]GitHubAccount    `json:"github_accounts,omitempty"`
	Connections   map[string]GitHubConnection `json:"github_connections,omitempty"`
	Outbox        map[string]SyncJob          `json:"outbox,omitempty"`
	Moves         map[string]Move             `json:"moves,omitempty"`
	AccessKeys    map[string]AccessKey        `json:"access_keys,omitempty"`
	Users         map[string]WikiUser         `json:"users,omitempty"`
	Teams         map[string]WikiTeam         `json:"teams,omitempty"`
	SourceMirrors map[string]SourceMirror     `json:"source_mirrors,omitempty"`
}

// CopyOrigin is retained on the destination for owner audit and is never
// returned on the anonymous public surface.
type CopyOrigin struct {
	SourceID       string `json:"source_id"`
	Path           string `json:"path"`
	Revision       int    `json:"revision"`
	SHA            string `json:"sha256"`
	DirectoryIndex bool   `json:"directory_index,omitempty"`
}

// ReconcileBase is an owner-only, versioned observation shared by two sources.
// It is not copy provenance: equal pre-existing articles can establish a base.
type ReconcileBase struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Revision int    `json:"revision"`
	SHA      string `json:"sha256"`
}

// ImportConflict records an incoming remote change that collided with a
// local revision, and its resolution.
type ImportConflict struct {
	Incoming    string `json:"incoming"`
	IncomingSHA string `json:"incoming_sha256"`
	BaseSHA     string `json:"base_sha256"`
	Commit      string `json:"commit"`
	Deleted     bool   `json:"deleted"`
	Created     string `json:"created_at"`
	Resolution  string `json:"resolution,omitempty"`
}

// BlobRef names a stored image blob by digest and media type.
type BlobRef struct {
	SHA  string `json:"sha256"`
	MIME string `json:"mime"`
}

// WikiMetadata is the content of a wiki source's veda.yaml.
type WikiMetadata struct {
	SchemaVersion int    `json:"schema_version" yaml:"schema_version"`
	Kind          string `json:"kind" yaml:"kind"`
	Title         string `json:"title" yaml:"title"`
	Description   string `json:"description,omitempty" yaml:"description,omitempty"`
	Image         string `json:"image,omitempty" yaml:"image,omitempty"`
	URL           string `json:"url,omitempty" yaml:"url,omitempty"`
}

// SyncJob is one outbox entry: a revision, and the images, links and
// bundled articles it depends on, waiting to be written to GitHub.
type SyncJob struct {
	ScopePrepared    bool               `json:"-"`
	ScopeBundle      bool               `json:"-"`
	ScopeBundlePaths map[string]bool    `json:"-"`
	ID               string             `json:"id"`
	SourceID         string             `json:"source_id"`
	Path             string             `json:"path"`
	Revision         int                `json:"revision"`
	SHA              string             `json:"sha256"`
	State            string             `json:"state"`
	Created          string             `json:"created_at"`
	Attempts         int                `json:"attempts"`
	LastError        string             `json:"last_error,omitempty"`
	PreparedCommit   string             `json:"prepared_commit,omitempty"`
	ObservedCommit   string             `json:"observed_commit,omitempty"`
	NextAttempt      string             `json:"next_attempt,omitempty"`
	Visibility       string             `json:"visibility"`
	Assets           map[string]BlobRef `json:"assets,omitempty"`
	Links            map[string]string  `json:"links,omitempty"`
	Bundle           map[string]string  `json:"bundle,omitempty"`
}

// Move records one explicit, revision-bound move; source history is retained.
type Move struct {
	ID                      string             `json:"id"`
	SourceID                string             `json:"source_id"`
	SourcePath              string             `json:"source_path"`
	SourceRevision          int                `json:"source_revision"`
	SourceConnectionID      string             `json:"source_connection_id,omitempty"`
	SourceRepositoryID      int64              `json:"source_repository_id,omitempty"`
	SourceBranch            string             `json:"source_branch,omitempty"`
	DestinationID           string             `json:"destination_id"`
	DestinationPath         string             `json:"destination_path"`
	DestinationConnectionID string             `json:"destination_connection_id,omitempty"`
	DestinationRepositoryID int64              `json:"destination_repository_id,omitempty"`
	DestinationBranch       string             `json:"destination_branch,omitempty"`
	SHA                     string             `json:"sha256"`
	State                   string             `json:"state"`
	PreparedDeletion        string             `json:"prepared_deletion,omitempty"`
	Assets                  map[string]BlobRef `json:"assets,omitempty"`
	Links                   map[string]string  `json:"links,omitempty"`
	Created                 string             `json:"created_at"`
}

// AccessKey grants current, read-only content from explicitly named sources.
// The bearer value is returned once and never stored, only its SHA-256 hash.
type AccessKey struct {
	ID      string   `json:"id"`
	Hash    string   `json:"hash"`
	Sources []string `json:"sources"`
	Expires string   `json:"expires_at"`
}

// WikiUser is a human account; its password hash never appears in views.
type WikiUser struct {
	ID           string   `json:"id"`
	Admin        bool     `json:"admin,omitempty"`
	SetupExpires string   `json:"setup_expires,omitempty"`
	Salt         string   `json:"salt"`
	PasswordHash string   `json:"password_hash"`
	Teams        []string `json:"teams"`
	Disabled     bool     `json:"disabled"`
	Revision     int      `json:"revision"`
	AuthVersion  int      `json:"auth_version"`
}

// Human identities are distinct from API/agent keys. Teams grant read-only
// access to named sources. Password hashes never appear in administrative views.
type WikiTeam struct {
	ID       string   `json:"id"`
	Sources  []string `json:"sources"`
	Revision int      `json:"revision"`
}

// GitHubAccount is a GitHub credential; the secret itself is stored outside
// the database and only referenced.
type GitHubAccount struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Login     string `json:"login"`
	SecretRef string `json:"secret_ref,omitempty"`
}

// GitHubConnection binds a source to a GitHub repository branch and records
// its verification and synchronization state.
type GitHubConnection struct {
	ID             string `json:"id"`
	Repository     string `json:"repository"`
	RepositoryID   int64  `json:"repository_id"`
	Branch         string `json:"branch"`
	AccountID      string `json:"account_id,omitempty"`
	Write          bool   `json:"write"`
	Private        bool   `json:"private"`
	Verification   string `json:"verification"`
	VerifiedAt     string `json:"verified_at"`
	State          string `json:"state"`
	LastError      string `json:"last_error,omitempty"`
	LastPull       string `json:"last_pull,omitempty"`
	NextAttempt    string `json:"next_attempt,omitempty"`
	Attempts       int    `json:"attempts"`
	Paused         bool   `json:"paused"`
	ConfigRevision int    `json:"config_revision"`
	TreeSHA        string `json:"tree_sha,omitempty"`
	// PendingHead is a remote head observed but not yet imported. Until a pull
	// imports it, the earlier public snapshot may be stale and stays hidden.
	PendingHead string `json:"pending_head,omitempty"`
}

// SourceMirror stores owner-controlled scheduling and sanitized status only.
// Article baselines and the durable GitHub outbox remain the source of truth.
type SourceMirror struct {
	SourceID      string   `json:"source_id"`
	DestinationID string   `json:"destination_id"`
	Enabled       bool     `json:"enabled"`
	Revision      int      `json:"revision"`
	State         string   `json:"state"`
	LastRun       string   `json:"last_run,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
	Blocked       int      `json:"blocked"`
	BlockedPaths  []string `json:"blocked_paths,omitempty"`
	More          bool     `json:"more"`
}

// Page is an article as served to a reader, with its rendered HTML and
// display metadata.
type Page struct {
	SearchText                string        `json:"-"`
	SearchSourceTitle         string        `json:"-"`
	ProgramDocumentation      bool          `json:"program_documentation,omitempty"`
	UnresolvedConflict        bool          `json:"unresolved_conflict,omitempty"`
	ConflictResolutionBlocked bool          `json:"conflict_resolution_blocked_by_write,omitempty"`
	Path                      string        `json:"path"`
	Title                     string        `json:"title"`
	Environment               string        `json:"environment"`
	SHA                       string        `json:"sha256"`
	Updated                   string        `json:"updated_at,omitempty"`
	Text                      string        `json:"-"`
	HTML                      template.HTML `json:"-"`
	// Assets are the images bound to the displayed revision, if any.
	Assets map[string]BlobRef `json:"-"`
}
