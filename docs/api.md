# REST API v1

JSON responses. Owner requests use `Authorization: Bearer <owner-key>`.
A separate `reader.key` bearer credential can read current private content but cannot write, read revision history, or access administration. The public listener ignores both keys. The default listener binds loopback. The key is generated into the private
data directory and is never returned by the API. Errors do not echo credentials
or article bodies. Do not put the key into URLs, browser storage or logs.

| Method | Route | Behavior |
| --- | --- | --- |
| GET | `/health` | Visible article count and visible revision timestamp |
| GET | `/api/state?q=&env=&page=` | React reading/search/tree response; `page` is source ID plus article path |
| GET | `/api/v1/sources` | Public sources, or all sources for owner |
| POST | `/api/v1/sources` | Create a local source (owner only) |
| GET | `/api/v1/articles` | Visible article index |
| GET | `/api/v1/articles?source_id=…&path=…` | Current revision of a visible article |
| GET | `/api/v1/articles?source_id=…&path=…&history=1` | All revisions (owner only; public callers still receive only current revision) |
| POST | `/api/v1/articles` | Create an article with expected revision 0 |
| PUT | `/api/v1/articles` | Update with the current expected revision |
| GET | `/api/v1/articles/translation-needed` | List program documents needing translation or publication review (owner only) |
| POST | `/api/v1/articles/translation` | Save a version-bound Russian translation (owner only) |
| POST | `/api/v1/moves/cancel` | Cancel a waiting move without removing either copy (owner only) |
| GET/POST | `/api/v1/moves` | List or start a revision-bound move from a standalone local or writable GitHub source (owner only) |
| GET/POST | `/api/v1/access-keys` | List scoped-key metadata or create a source-scoped read-only key (owner only) |
| POST | `/api/v1/access-keys/revoke` | Revoke a scoped key by ID (owner only) |
| POST | `/api/v1/articles/copy` | Copy a revision to a new source and retain provenance (owner only) |
| POST | `/api/v1/import` | Owner-only bounded, atomic repository import batch |
| GET | `/api/v1/conflicts` | Owner-only unresolved import conflicts |
| POST | `/api/v1/conflicts/resolve` | Owner-only conflict resolution with expected revision |
| GET/POST | `/api/v1/github/accounts` | List or add explicitly selected GitHub credentials (owner only) |
| GET/POST/PUT | `/api/v1/github/connections` | List, connect or reconfigure repositories (owner only) |
| GET | `/api/v1/github/outbox` | List queued write jobs (owner only) |
| POST | `/api/v1/github/sync`, `/api/v1/github/pause` | Schedule sync or change pause state (owner only) |
| POST | `/api/v1/github/publish-job` | Explicitly approve a queued private edit for publication (owner only) |
| GET | `/media/{source_id}/{path}` | Visible imported raster image with digest verification |

Source body:

```json
{"id":"handbook","title":"Handbook","kind":"wiki","visibility":"private"}
```

`id` is a lowercase alphanumeric/hyphen/underscore slug, at most 64 characters.
`kind`: `wiki` or `program`. `visibility`: `public` or `private`. Local source visibility and identity are immutable. Connected source visibility
follows verified GitHub repository visibility. Source creation is an explicit local
administrative action, not GitHub import or remote permission verification.

Article body:

```json
{"source_id":"handbook","path":"guide.md","text":"# Guide\n","expected_revision":0}
```

An update uses PUT and `expected_revision: 1` after the first successful write.
Responses include `revision`, `sha256`, `created_at` and the saved `text`.
Creation returns 201; update returns 200. Revision mismatch returns 409;
unavailable/unauthorized article returns 404; invalid input returns 400;
unauthorized writes return 403; storage failures return 500. Unknown fields and
trailing JSON are rejected. Copying retains the original. Move restrictions are described below.

Scoped reader keys grant current read-only content only for explicitly listed
sources. Create with an owner-authorized POST to `/api/v1/access-keys` containing
`id`, `sources` and `expires_at` (RFC3339, future and at most 366 days). The
response returns the bearer key once; only its hash is stored. Revoke by POSTing
the `id` to `/api/v1/access-keys/revoke`. The list endpoint returns metadata,
never the key or hash. Scoped keys are ignored on the public listener and cannot
write, access history or use owner-only endpoints. Existing shared reader and
Unix socket credentials still grant all private sources until separately changed.
Key issuance upgrades the store to version 4, which older servers refuse to open.
The Unix reader socket remains implicitly trusted by default; use
`--reader-socket-require-key` and configure scoped bearer keys before relying on
per-agent restrictions. An invalid or revoked bearer on that socket does not
fall back to implicit legacy access.

Program article paths must start with `docs/`. Absolute paths, traversal, hidden
components, backslashes and non-Markdown paths are refused. Paths are logical
keys inside the store, never arbitrary filesystem paths. Rendered Markdown
disables raw HTML and dangerous link schemes. There is no CORS write interface.

For retries, reread after an ambiguous response: an accepted write might already
be durable. The revision precondition prevents duplicate writes or stale updates.
This is optimistic concurrency, not an idempotency-key implementation. Unknown
source IDs cannot create implicit sources. Connected writable sources queue writes
for background synchronization; see [GitHub synchronization](github.md).

An import batch can explicitly create its source with `create: true`; it never
implicitly creates a source from an article write. Import details and metadata
schema: [repository import](import.md). Managed source metadata/provenance and
assets are read-only through the ordinary source-creation endpoint.

## Copy across sources

`POST /api/v1/articles/copy` requires `source_id`, `source_path`,
`expected_revision`, `destination_id`, `destination_path` and optionally
`confirm_publication`. The destination must be a new path in a different
source. Conflicts or stale source revisions are rejected. The copy and its
remote outbox job are committed atomically; inspect the outbox to verify remote
durability. The source is retained. Private-to-public copying requires explicit
review and `confirm_publication: true`; obvious credential-like text is refused.
A connected destination with stale visibility proof stays unpublished and its
queued write needs later approval if the destination is verified public.
Owner-only provenance is preserved locally and removed from public responses.
Copy does not copy referenced assets or rewrite Markdown links, and is not a
complete move operation.

## Restricted local snapshot import

On Linux, the optional `--local-import-source ID` works only with
`--reader-socket`. The kernel must identify the Unix socket peer as UID 0. It
accepts only `POST /api/v1/import` without a browser Origin, and only a partial
batch (`complete: false`) for that exact existing, private, disconnected local
source. It cannot create sources, operate GitHub, write individual articles or
change visibility. The same generation and conflict rules apply. Non-root peers
remain read-only; HTTP headers cannot supply peer identity. On other operating
systems this import channel is unavailable. A privileged process can already
access local files; do not treat this mechanism as per-user authorization.

## Import observation

Authenticated source listings include `last_import_observed_at` when a local
import was successfully accepted since this server process started, including
an unchanged import. A failed import does not advance it. The value is kept in
memory and is absent after restart until another successful import; it is not
a claim that the upstream checkout or infrastructure is currently correct.
`unresolved_conflicts` counts pending conflicts. `import_partial` and
`import_dirty` indicate known coverage and source-state limits. These fields
are omitted from public-only listings where applicable. MCP `veda_context`
returns the same status alongside scope instructions.

## Move between sources

`POST /api/v1/moves` takes `source_id`, `source_path`, `expected_revision`,
`destination_id`, `destination_path`, `confirm_move: true` and optionally
`confirm_publication`. Manually maintained local sources and synchronized,
writable GitHub sources are accepted; independent local imports are not.
Relative Markdown images can be copied with the article into a standalone local
or writable GitHub target; existing images at the target path must have the
same digest. The GitHub outbox commits the Markdown and its images together,
then confirms them in a complete pull before allowing source deletion. A partial
target import blocks the bundle. Relative links to other articles and embedded
HTML remain unsupported. A stored translation can be moved only to another program source;
it is copied as a separate private draft and requires publication review even
when the target is public. The target must be a new path in another source.
If two source IDs refer to the same GitHub repository and branch, moving to
the same physical path is rejected even when one source view is stale.

The destination and durable move record are created in one transaction. For a
local target, the source is tombstoned in that transaction; for a connected
target, the source remains visible until a later pull observes the queued
revision in the same repository and branch. For a GitHub source, VEDA rechecks
the destination and source tree, prepares a non-force deletion commit with an
exact expected branch head and
waits for a complete pull to observe source removal. An outage or write-disabled target
leaves the move waiting. If either article changes, its source identity changes,
or a destination conflict appears, the source stays and the move reports
`source_changed` or `destination_changed`. Source revision history remains in
the store. Inspect `GET /api/v1/moves` or `veda_moves`; an accepted move is not
necessarily complete. A waiting move can be cancelled through `/api/v1/moves/cancel`
or `veda_cancel_move` with `confirm_cancel: true` before source deletion is
prepared. Both copies remain; an already queued destination write may still
finish. Once deletion is prepared, its response can be uncertain and cancellation
is refused until synchronization resolves it. A definitive rejection of the
first ref update restores the cancellable waiting state; a later rejection
cannot resolve an earlier uncertain attempt. The two remote repositories
cannot be updated atomically; external edits may occur between checks. Local
source revision history is retained. A change to either translation while the
move is waiting prevents source removal. Private-to-public publication requires
separate explicit confirmation. Relative article links are not yet supported
for transfers. Live
acceptance with credentials stored by VEDA remains pending.

Creating the first local-source move upgrades the local store format to version 2;
the first GitHub-source move upgrades it to version 3. Older
versions of VEDA reject that store instead of silently discarding move records.
The previous version-2 store reader was tested against a synthetic version-3
file and refused it without changing it. A move with a translation upgrades the
store to version 5; older binaries reject it rather than discard the additional
move invariant. An older binary refused a synthetic v5 store without changing
it. A synthetic v5 store with a translation and scoped key also passed a PBS
backup/restore round-trip; production v5 backup and restore have not been
validated. A move containing images to a local target upgrades the store to
version 6; a copy or move with images to a connected target upgrades it to
version 7. Older binaries reject these stores. A synthetic v7 store with a
paused image outbox passed a PBS round-trip; its restored queue then completed
against a local fake GitHub. Production v6/v7 backup and restore are unverified.
Restore a compatible binary or a separately verified backup when rolling back;
do not overwrite newer state with an older server.
