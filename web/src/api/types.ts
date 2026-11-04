// Wire types of the VEDA HTTP API. Field names and casing are exactly the
// JSON keys the Go server emits (internal/domain, internal/httpapi, internal/auth).
// A Go nil slice or pointer is serialized as null, so those fields are `| null`.

/** An article as served to a reader (domain.Page). */
export interface Page {
  path: string;
  title: string;
  environment: string;
  sha256: string;
  updated_at?: string;
  program_documentation?: boolean;
  unresolved_conflict?: boolean;
  conflict_resolution_blocked_by_write?: boolean;
}

/** The pages of one source (httpapi.Group). */
export interface Group {
  Name: string;
  ID: string;
  Count: number;
  Pages: Page[] | null;
  Description?: string;
  SourceURL?: string;
}

/** A recently updated page on the home view (httpapi.Card). */
export interface Card {
  Page: Page;
  Image: string;
  Description: string;
  Tag: string;
  ImageURL: string;
}

/** An owner-only notice that a source needs attention (httpapi.SourceWarning). */
export interface SourceWarning {
  ID: string;
  Name: string;
  Partial: boolean;
  Dirty: boolean;
  Conflicts: number;
  Withheld: number | null;
  SyncIssue?: string;
  LastImportObservedAt?: string;
}

/** A link between two pages (render.Edge). */
export interface GraphEdge {
  from: string;
  to: string;
}

/** The reading interface state (httpapi.View). */
export interface StateView {
  GraphEdges: GraphEdge[] | null;
  Cards: Card[] | null;
  SourceWarnings: SourceWarning[] | null;
  Public: boolean;
  Groups: Group[] | null;
  Pages: Page[] | null;
  Page: Page | null;
  Query: string;
  Selected: string;
  Generated: string;
  Withheld: number;
  Count: number;
  GitHubCount: number;
  TOC: string;
}

/** The body of GET /api/state. */
export interface StatePayload {
  view: StateView;
  html: string;
  toc: string;
}

/** A reader key as listed (auth.AccessKeyView); the secret is never listed. */
export interface AccessKey {
  id: string;
  sources: string[];
  expires_at: string;
  etag?: string;
}

/** The one-time response to creating a key; `key` is shown exactly once. */
export interface IssuedKey {
  id: string;
  sources: string[];
  expires_at: string;
  key: string;
}

/** A human account (auth.UserView). */
export interface WikiUser {
  id: string;
  admin: boolean;
  teams: string[] | null;
  disabled: boolean;
  revision: number;
}

/** A group of users that shares source access (domain.WikiTeam). */
export interface WikiTeam {
  id: string;
  sources: string[] | null;
  revision: number;
}

/** A source an access key or team may be granted (GET /api/access/sources). */
export interface SourceChoice {
  id: string;
  title: string;
  kind: string;
  visibility: string;
}

/** A stored GitHub credential reference (domain.GitHubAccount). */
export interface GitHubAccount {
  id: string;
  kind: string;
  login: string;
  secret_ref?: string;
}

/** A source bound to a GitHub repository branch (domain.GitHubConnection). */
export interface GitHubConnection {
  id: string;
  repository: string;
  repository_id: number;
  branch: string;
  account_id?: string;
  write: boolean;
  private: boolean;
  verification: string;
  verified_at: string;
  state: string;
  last_error?: string;
  last_pull?: string;
  next_attempt?: string;
  attempts: number;
  paused: boolean;
  config_revision: number;
  tree_sha?: string;
  pending_head?: string;
}

/** Scheduling and sanitized status of a source mirror (domain.SourceMirror). */
export interface SourceMirror {
  source_id: string;
  destination_id: string;
  enabled: boolean;
  revision: number;
  state: string;
  last_run?: string;
  last_error?: string;
  blocked: number;
  blocked_paths?: string[];
  more: boolean;
}

/** Answers of the session endpoints (login, logout, status). */
export interface SessionStatus {
  authenticated: boolean;
  via_reader_session?: boolean;
}

/** The reader profile (GET /api/session/profile). */
export interface SessionProfile {
  password_change: boolean;
}
