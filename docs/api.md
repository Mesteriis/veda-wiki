# REST API v1

JSON responses. Owner requests use `Authorization: Bearer <owner-key>`.
A separate `reader.key` bearer credential can read current private content but cannot write, read revision history, or access administration. The public listener ignores both keys. The default listener binds loopback. The key is generated into the private
data directory and is never returned by the API. Errors do not echo credentials
or article bodies. Do not put the key into URLs, browser storage or logs.

For a program document, the reading page includes `translation_state`: `current`
when a matching translation is displayed, `unavailable` when none is visible,
and `stale` to authenticated readers when an existing translation targets an
older original. The public surface reports `unavailable` in that last case
and does not expose the existence or origin of unpublished translations.
Unknown translation origin is explicitly labelled in the interface.

| Method | Route | Behavior |
| --- | --- | --- |
| GET | `/health` | Visible article count and visible revision timestamp |
| GET | `/api/state?q=&env=&page=` | React reading/search/tree response; `page` is source ID plus article path. Authenticated readers receive warnings for incomplete or dirty imports and unresolved conflicts in their visible sources |
| GET | `/api/v1/search?q=&source_id=&limit=&offset=` | Compact, paginated search across visible articles |
| GET | `/api/v1/articles/excerpt?source_id=&path=&offset=&limit=&expected_sha256=` | Read up to 16,000 Unicode code points of the current visible revision; continuation requires the first response's SHA |
| GET | `/api/v1/articles/display?source_id=&path=` | Read the displayed Markdown, translation status and original SHA under the caller's source permissions |
| GET | `/api/v1/articles/display-excerpt?source_id=&path=&offset=&limit=&expected_sha256=&expected_original_sha256=` | Read up to 16,000 Unicode code points of displayed Markdown; continuation requires both hashes from the first response |
| GET | `/api/v1/sources` | Public sources, or all sources for owner |
| GET | `/api/v1/context?source_id=…&path=…` | Visible ancestor instructions and source status in one snapshot |
| POST | `/api/v1/sources` | Create a local source (owner only) |
| GET | `/api/v1/articles` | Visible article index |
| GET | `/api/v1/articles?source_id=…&path=…` | Current revision of a visible article |
| GET | `/api/v1/articles?source_id=…&path=…&history=1` | All revisions (owner only; public callers still receive only current revision) |
| POST | `/api/v1/articles` | Create an article with expected revision 0 |
| PUT | `/api/v1/articles` | Update with the current expected revision |
| GET | `/api/v1/articles/translation-needed` | List program documents needing translation or publication review, limited to the reader's authorized sources |
| POST | `/api/v1/articles/translation-validate` | Validate a revision-bound translation without saving; owner or authorized reader, no browser Origin |
| POST | `/api/v1/articles/translation` | Save a version-bound Russian translation (owner only) |
| POST | `/api/v1/moves/cancel` | Cancel a waiting move without removing either copy (owner only) |
| GET/POST | `/api/v1/moves` | List or start a revision-bound move from a standalone local or writable GitHub source (owner only) |
| GET/POST | `/api/v1/access-keys` | List, create or update a source-scoped read-only key (owner only) |
| GET/POST | `/api/v1/teams`, `/api/v1/users` | List, create or update reader teams and named users (owner only) |
| POST | `/api/v1/access-keys/revoke` | Revoke a scoped key by ID (owner only) |
| GET/POST | `/api/access/session/status`, `/api/access/session/login`, `/api/access/session/logout` | Separate browser access-administration session |
| GET | `/api/access/sources` | List source metadata for the separate browser access-administration session |
| GET/POST | `/api/access/teams`, `/api/access/users` | Manage teams and named users in the separate access-administration session |
| GET | `/api/session/profile` | Report whether the current reader session can change its password |
| POST | `/api/session/password` | Change the current user's password after confirming the current one |
| GET/POST | `/api/access/keys` | List, create or update scoped keys in that browser session |
| POST | `/api/access/keys/revoke` | Revoke a scoped key in that browser session |
| POST | `/api/v1/articles/copy` | Copy a revision to a new source and retain provenance (owner only) |
| POST | `/api/v1/articles/transfer-plan` | Inspect recursive relative article links and destination matches (owner only) |
| POST | `/api/v1/import` | Owner-only bounded, atomic repository import batch |
| GET | `/api/v1/conflicts` | Owner-only unresolved import conflicts |
| POST | `/api/v1/conflicts/resolve` | Owner-only conflict resolution with expected revision |
| GET/POST | `/api/v1/github/accounts` | List or add explicitly selected GitHub credentials (owner only) |
| GET/POST/PUT | `/api/v1/github/connections` | List, connect or reconfigure repositories (owner only) |
| GET | `/api/v1/github/outbox` | List queued write jobs (owner only) |
| POST | `/api/v1/github/sync`, `/api/v1/github/pause` | Schedule sync or change pause state (owner only) |
| POST | `/api/v1/github/publish-job` | Explicitly approve a queued private edit for publication (owner only) |
| POST | `/api/v1/github/cancel-bundle` | Cancel an unprepared linked-article bundle (owner only) |
| GET | `/media/{source_id}/{path}` | Visible imported raster image with digest verification |

Search matches exact phrases or meaningful words across visible titles, paths
and article text. For translated articles it searches both the visible original
revision and its visible translation, scores each text independently, and
returns the displayed title. An unapproved translation is never searched on the
public surface. Multiword queries require two distinct meaningful matches;
title and path matches rank above body matches. A small set of infrastructure
aliases covers generic container terms, LXC and Docker, as well as VPS/VDS/VSP.
Generic container queries include both technologies; explicit LXC and Docker
queries remain distinct. This is lexical search, not
semantic retrieval, general typo correction or authorization to act. The
query is limited to 300 bytes and up to 16 meaningful terms are considered.
`/api/v1/search` and `veda_find` return only source ID, relative path and title,
with `total` and `offset` calculated after visibility filtering. Limits are
1–100 (default 20). An inaccessible source filter returns an empty result.
Pagination is not a snapshot: repeat a search if sources change between pages.
Each individual search ranks titles and bodies from one in-memory snapshot;
public results use only revisions visible to the public at that snapshot.

Page objects expose `updated_at`, the timestamp of the revision displayed on
that surface. Public pages use the published revision or approved translation,
not timestamps from unpublished private edits. The home cards are sorted by
this value; it is not the filesystem modification time or proof that the
upstream repository or infrastructure was recently checked.
Public single-article responses include only the visible revision; conflict
records, upstream hashes and internal publication snapshots are omitted for
both local and GitHub-connected sources.

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
never the key or stored hash. To update an existing key, POST `id`, `sources`,
`expires_at` and the `expected_etag` from that list. A stale fingerprint or an
expired key returns 409; expired keys must be reissued. The existing bearer is
unchanged and is not returned. Scope changes apply to subsequent bearer and
browser-session requests; requests already in progress can finish with their
previous scope. Scoped keys are ignored on the public listener and cannot
write, access history or use owner-only endpoints. Existing shared reader and
Unix socket credentials still grant all private sources until separately changed.
Key issuance upgrades the store to version 4, which older servers refuse to open.

For named readers, create a team with `POST /api/v1/teams` and `id`, `sources`;
create a user with `POST /api/v1/users` and `id`, `teams`, `password`. An empty
team list grants no private source. To update either object, include its current
`expected_revision`; user updates can change `teams`, `disabled` or `password`.
Users cannot manage their own team membership. A user changes their own password
with `current_password` and `new_password` through the same-origin browser
session endpoint. Team changes affect the next request; disabling a user or
changing their password invalidates all their sessions. The APIs do not return
passwords, salts or hashes. An owner can disable a user; account deletion and
team deletion are not currently supported. Creating either object upgrades the
logical store to version 15, which older binaries must not open.
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
`confirm_publication` and `include_linked`. The destination must be a new path in a different
source. Conflicts or stale source revisions are rejected. The copy and its
remote outbox job are committed atomically; inspect the outbox to verify remote
durability. The source is retained. Private-to-public copying requires explicit
review and `confirm_publication: true`; obvious credential-like text is refused.
An article hidden from public readers by an incomplete source import also
requires explicit publication review before copy or move to a public source.
A connected destination with stale visibility proof stays unpublished and its
queued write needs later approval if the destination is verified public.
Owner-only provenance is preserved locally and removed from public responses.
Copy transfers supported relative images and preserves relative article links
only when the destination already contains the same linked revision. It does
not automatically copy linked articles unless `include_linked: true` is set.
With that flag, all recursively linked Markdown articles and supported images
are copied atomically into a standalone local destination, or queued together
for one GitHub commit. Cycles are handled;
existing matching articles are retained and conflicts reject the whole batch.
Each newly copied article retains source provenance and its translation is a
private draft. Approval for a private-to-public batch covers every article;
each is checked for credential-like text. Independently imported destinations
cannot receive a linked batch. Copy never deletes the source.

For a connected destination, the group is persisted locally in store format v9 or newer,
which older binaries reject. The outbox job is complete only after a complete,
non-withheld pull observes every article and image at the expected digest.
The group cannot be locally edited or moved while pending. An owner can cancel
a never-prepared group with `POST /api/v1/github/cancel-bundle` (`job_id`,
`confirm_cancel: true`) or `veda_cancel_bundle`; local copies remain. Once a
commit has been prepared, its result may be uncertain and cancellation is
refused until synchronization resolves it. A failed group may need retry or
conflict resolution. This workflow has synthetic tests only; restoration of a
production v9 store and live GitHub credentials have not been verified.

`POST /api/v1/articles/transfer-plan` and `veda_transfer_plan` accept the same
source and destination identifiers, paths, and expected source revision. They
return the recursively linked Markdown articles with their current revisions,
digests, mapped paths and destination status (`missing`, `matching`, or
`conflict`). Cycles are included once; ambiguous path mappings are rejected.
The plan is a read-only snapshot, not a reservation or a guarantee that a
copy or move will succeed. It does not inspect images, embedded HTML,
publication approval, remote write permissions or later changes. It does not
automatically copy missing articles; execution must revalidate all conditions.

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

Authenticated source listings include `last_import_observed_at` when an import
was successfully accepted since this server process started, including an
unchanged local import or a GitHub branch head freshly verified to match the
imported snapshot. The latter does not re-download every blob. A failed import
or stale connection observation does not advance it. The value is kept in
memory and is absent after restart until another successful import; it is not
a claim that the upstream checkout or infrastructure is currently correct.
`unresolved_conflicts` counts pending conflicts. `last_import_withheld`, when
present for an authenticated reader, reports the number of items withheld or
unavailable in the last accepted import attempt. It is not an exhaustive count
of paths outside a curated import's scope. The count is kept in memory and is
absent after restart until another import; a head-only check does not create it.
`import_partial` and `import_dirty` indicate known coverage and source-state limits. These fields
are omitted from public-only listings where applicable. MCP `veda_context`
returns the same status alongside scope instructions. For authenticated access
to a connected source, it also includes `sync_state`, `sync_verification` and,
when present, the sanitized `sync_error` code. A failed, paused or unverified
connection can leave the local copy stale even when an earlier observation time
is present. These connection fields are omitted from public context responses
and from local disconnected sources.
The context endpoint reads all ancestor `AGENTS.md` articles and status under
one lock, subject to the caller's source permissions and published revisions.
Missing instructions only mean they are absent from the visible dataset; a
partial import may omit files. Instruction text is limited to 1.5 MB and the
encoded response to less than 2 MB; excess returns HTTP 413. Instructions are
documentation, not authorization or proof of current infrastructure state.
The MCP adapter and server must both support this endpoint when upgraded.

## Move between sources

`POST /api/v1/moves` takes `source_id`, `source_path`, `expected_revision`,
`destination_id`, `destination_path`, `confirm_move: true` and optionally
`confirm_publication` and `include_linked`. With `include_linked: true`, the
linked graph is copied to a standalone local destination or queued as one
GitHub commit before removing the requested source article; other source
articles remain. A connected destination must be observed in a complete pull,
and a GitHub source is deleted only after the destination and its dependencies
are rechecked. Cancelling a never-prepared destination bundle marks the move
`destination_changed` and keeps the source.
The first graph move to a connected destination upgrades the store to v10;
older binaries reject it. Production v10 restore has not been verified.
Manually maintained local sources and synchronized,
writable GitHub sources are accepted; independent local imports are not.
Relative and repository-root Markdown images can be copied with the article into a standalone local
or writable GitHub target; existing images at the target path must have the
same digest. The GitHub outbox commits the Markdown and its images together,
then confirms them in a complete pull before allowing source deletion. A partial
target import blocks the bundle. A relative or repository-root article link is accepted when its
destination path already has the same current article revision; a public target
also requires that revision to be published. The linked article is checked
again before source deletion. Other relative links and embedded HTML remain
unsupported. A stored translation can be moved only to another program source;
it is copied as a separate private draft and requires publication review even
when the target is public. The target must be a new path in another source.
If two source IDs refer to the same GitHub repository and branch, moving to
the same physical path is rejected even when one source view is stale.

Pending work that retains repository-root image or link dependencies upgrades
the store to v12; earlier binaries reject that version rather than silently
dropping those dependencies. Preserve and test the matching backup before any
rollback. Production v12 restore has not been verified.

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
is refused until synchronization resolves it. During this interval, local edits,
translation changes, conflict resolution and moves of the source, destination
and recorded linked destination articles are refused. External changes in a
GitHub repository cannot be locked by VEDA. A definitive rejection of the
first ref update restores the cancellable waiting state; a later rejection
cannot resolve an earlier uncertain attempt. The two remote repositories
cannot be updated atomically; external edits may occur between checks. Local
source revision history is retained. A change to either translation while the
move is waiting prevents source removal. Private-to-public publication requires
separate explicit confirmation. Without `include_linked`, linked articles are
not copied automatically; missing or different targets must be synchronized first. Live
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
version 7. Moves with linked articles, and connected copies with linked
articles, upgrade the store to version 8. Older binaries reject these stores.
A synthetic v7 store with a
paused image outbox passed a PBS round-trip; its restored queue then completed
against a local fake GitHub. Production v6/v7/v8 backup and restore are unverified.
Restore a compatible binary or a separately verified backup when rolling back;
do not overwrite newer state with an older server.
