# REST API v1

JSON responses. Owner requests use `Authorization: Bearer <owner-key>`.
A separate `reader.key` bearer credential can read current private content but cannot write, read revision history, or access administration. The public listener ignores both keys. The default listener binds loopback. The key is generated into the private
data directory and is never returned by the API. Errors do not echo credentials
or article bodies. Do not put the key into URLs, browser storage or logs.

For an authenticated reader, the selected page includes
`unresolved_conflict: true` when that article has an unresolved import conflict.
The field is absent for public readers and for articles without a conflict.

| Method | Route | Behavior |
| --- | --- | --- |
| GET | `/health` | Visible article count and visible revision timestamp |
| GET | `/api/state?q=&env=&page=` | React reading/search/tree response; `page` is source ID plus article path. Authenticated readers receive warnings for incomplete or dirty imports, unresolved conflicts and unverified, paused or failed GitHub synchronization in their visible sources. A conflicted article includes `conflict_resolution_blocked_by_write` when a prepared write or linked bundle prevents resolution; public responses omit it |
| GET | `/api/v1/search?q=&source_id=&limit=&offset=` | Compact, paginated search across visible articles |
| GET | `/api/v1/articles/excerpt?source_id=&path=&offset=&limit=&expected_sha256=` | Read up to 16,000 Unicode code points of the current visible revision; continuation requires the first response's SHA |
| GET | `/api/v1/articles/proposal-base?source_id=&path=` | Authenticated source-scoped revision, SHA, unresolved import-conflict flag and source status in one snapshot |
| GET | `/api/v1/articles/new-proposal-base?source_id=&path=` | Authenticated source-scoped visible-path check and source status in one snapshot; does not reserve the path |
| GET | `/api/v1/sources` | Public sources, or all sources for owner |
| GET | `/api/v1/context?source_id=…&path=…` | Visible ancestor instructions and source status in one snapshot |
| POST | `/api/v1/sources` | Create a local source (owner only) |
| GET | `/api/v1/articles` | Visible article index |
| GET | `/api/v1/articles?source_id=…&path=…` | Current revision of a visible article |
| GET | `/api/v1/articles?source_id=…&path=…&history=1` | Stored revisions: the newest three plus protected ones, so numbers can start above 1 and have gaps (owner only; public callers still receive only the current revision) |
| POST | `/api/v1/articles` | Create an article with expected revision 0 |
| PUT | `/api/v1/articles` | Update with the current expected revision |
| PATCH | `/api/v1/articles` | Replace one fragment of the current revision: `old_text` must occur exactly once, `new_text` may be empty; answers revision, SHA and warnings without the text, or 422 `fragment_not_found` / `fragment_ambiguous` |
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
| POST | `/api/v1/sources/reconcile` | Reconcile bounded changes between separate private sources (owner only) |
| GET, POST | `/api/v1/sources/mirrors` | Inspect or configure periodic private reconciliation (owner only) |
| POST | `/api/v1/articles/transfer-plan` | Inspect recursive relative article links and destination matches; owner or scoped reader with access to both sources, no browser Origin |
| POST | `/api/v1/import` | Owner-only bounded, atomic repository import batch |
| GET | `/api/v1/conflicts` | Owner-only unresolved import conflicts |
| POST | `/api/v1/conflicts/resolve` | Owner-only conflict resolution with expected revision |
| GET/POST | `/api/v1/github/accounts` | List or add explicitly selected GitHub credentials (owner only) |
| GET/POST/PUT | `/api/v1/github/connections` | List, connect or reconfigure repositories (owner only) |
| GET | `/api/v1/github/outbox` | List queued write jobs (owner only) |
| POST | `/api/v1/github/sync`, `/api/v1/github/pause` | Schedule sync or change pause state (owner only) |
| POST | `/api/v1/github/publish-job` | Explicitly approve a queued private edit for publication (owner only) |
| POST | `/api/v1/github/cancel-bundle` | Cancel an unprepared linked-article bundle (owner only) |
| POST | `/api/public/access-requests` | A visitor's request for API/MCP access to public sources: `sources`, `telegram` (`@nick`), `comment`; same origin, any listener, rate limited (see below) |
| GET/POST | `/api/access/requests`, `/api/access/requests/approve`, `/api/access/requests/reject` | List requests, issue a read key for one (once, like key creation) or reject it, in the access-administration session |
| GET/POST | `/api/access/telegram`, `/api/access/telegram/events`, `/api/access/telegram/test` | Bot status and token (never returned), event switches, a test message, in the access-administration session |
| POST | `/api/session/telegram` | Set the signed-in user's own Telegram ID (`0` removes it); `GET /api/session/profile` returns it |
| POST | `/mcp` | Read-only MCP over Streamable HTTP for a source-scoped access key (`Authorization: Bearer`); private listeners only — see [agent workflow](agent-workflow.md#remote-mcp-by-address-and-key) |
| GET | `/api/v1/mcp/tools` | MCP tools with the modes that list them: `reader`, `editor`, `owner` (owner only; `/api/admin/mcp/tools` in an administrator session) |
| GET | `/api/v1/mcp/activity` | When each MCP mode last reached this process, and the 20 newest revisions written through MCP (owner only; `/api/admin/mcp/activity` in an administrator session) |
| GET | `/media/{source_id}/{path}` | Visible imported raster image with digest verification |

Direct article creation and update reject credential-like text with HTTP 400
when the source is currently public; private writes remain available. The check
can miss secrets or flag harmless examples. GitHub synchronization also blocks
credential-like queued Markdown for both public and private repositories; a
corrected new revision supersedes an unprepared blocked job without a bundled
article graph, retaining still-referenced image and link dependencies. This
does not retroactively remove already published content or resolve an ambiguous
prepared push. See [GitHub synchronization](github.md) for queue recovery.

Search matches exact phrases or meaningful words across visible source titles, article titles, paths
and article text. Multiword queries require two distinct meaningful matches;
source title, article title and path matches rank above body matches. A small set of infrastructure
aliases covers generic container terms, LXC and Docker, as well as VPS/VDS/VSP.
Generic container queries include both technologies; explicit LXC and Docker
queries remain distinct. This is lexical search, not
semantic retrieval, general typo correction or authorization to act. The
query is limited to 300 bytes and up to 16 meaningful terms are considered.
`/api/v1/search` and `veda_find` return source ID, relative path, title and,
when a body line matches, `match`: its 1-based `line`, the code-point `offset`
of the line start (for `/api/v1/articles/excerpt`), the nearest `section`
heading outside fenced code, the line `text` cut to about 200 characters and
the revision `sha256`, which an excerpt starting at `offset` needs as
`expected_sha256`.
The whole query wins over single terms, then the earliest line with the most
terms; only the returned page is scanned for lines. `total` and `offset` are
calculated after visibility filtering. Limits are
1–100 (default 20). An inaccessible source filter returns an empty result.
Pagination is not a snapshot: repeat a search if sources change between pages.
Each individual search ranks titles and bodies from one in-memory snapshot;
public results use only revisions visible to the public at that snapshot.

Page objects expose `updated_at`, the timestamp of the revision displayed on
that surface. Public pages use the published revision,
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
An unresolved import conflict also causes a write to return 409 even if the
local revision has not changed. Resolve the conflict first; prepared MCP
proposals report the conflict and source status observed during preparation,
which may change before a write. `conflict_resolution_blocked_by_write` reports
whether the selected article has a nonterminal prepared write or linked bundle;
it does not expose other paths or indicate that an ordinary write is blocked.
`write_blocked_by_move` separately indicates that an active source-deletion
phase locks this article, including destination dependencies. The scoped result
does not disclose the other endpoint or move details; it can change before a
write. `write_blocked_by_bundle` indicates a linked group also blocks normal
writes to the selected article.
The MCP adapter requires a server that returns
this status in the proposal-base response. New-article proposals use
`new-proposal-base`; the path check and source status come from one snapshot.
Deleted history remains hidden and can still cause a later write conflict.
The updated adapter requires a server supporting this endpoint.
Responses include `revision`, `sha256`, `created_at` and the saved `text`.
A revision (in write responses, `versions` and the public visible version)
may also include `assets`: the images its text referenced when it was written,
as repository path → `{sha256, mime}`, limited to images the source had then.
It is omitted when there are none and for revisions written before images were
bound. Rendered pages load such an image as `/media/<source>/<path>?v=<sha256>`;
that URL answers only for a version the reader may see (the owner: any stored
version; other private readers: the latest revision; the public listener: the
public version of a publicly readable source) and is otherwise the same 404 as an
unknown image.
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
drops dangerous link schemes and all raw HTML except an allow list: `<details>`
(with optional `open`), `<summary>`, `<kbd>`, `<sub>`, `<sup>` and `<br>`, never
with other attributes. It also renders GitHub alerts (`> [!NOTE]`), footnotes
and definition lists, and hides a leading YAML front matter block, whose
`title:` becomes the article title. Fenced code is highlighted on the server
with CSS classes only; a ```` ```mermaid ```` fence is sent as escaped source in
`<pre class="mermaid">`. On such pages the browser opens a hidden
`/diagram-frame.html`, which loads Mermaid and draws each diagram; the page
shows the result as an isolated SVG image. Mermaid needs inline styles while
it lays a diagram out, so only that frame's Content-Security-Policy allows
them: it still runs only the interface's own scripts, reaches no network and
can be framed only by the interface. Every other page keeps forbidding inline
styles. An invalid diagram keeps its source text.

A successful `POST` or `PUT` of an article also returns `warnings` when the
text uses Markdown that the reader will not see as written; the revision is
saved either way. Each warning has `code`, the 1-based `line` (front matter
included) and `message`: `raw_html` (a tag outside the allow list or with
attributes), `liquid` (`{% %}` or `{{ }}` outside code), `link_outside_source`,
`image_outside_source`, `image_remote` (blocked by the page policy) and
`code_without_language`. At most 50 are returned. The MCP tool
`veda_format_guide` returns the full formatting reference. There is no CORS write interface.

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
`confirm_publication`, `include_linked`, and `expected_destination_revision`. The
destination is in a different source. An existing destination is updated only
when `expected_destination_revision` matches its current revision; its history
and upstream baseline are retained. Omit it or use zero for a new destination.
Updating an existing destination cannot be combined with `include_linked`.
Conflicts or stale revisions are rejected. The copy and its
remote outbox job are committed atomically; inspect the outbox to verify remote
durability. The source is retained. Private-to-public copying requires explicit
review and `confirm_publication: true`. A public destination refuses
credential-like text even when the source revision was already public; this
also applies to linked articles and moves.
An article hidden from public readers by an incomplete source import also
requires explicit publication review before copy or move to a public source.
A connected destination with stale visibility proof stays unpublished and its
queued write needs later approval if the destination is verified public.
Owner-only provenance is preserved locally and removed from public responses.
Copy transfers supported relative images and preserves relative article links
only when the destination already contains the same linked revision. It does
not automatically copy linked articles unless `include_linked: true` is set.
Directory links such as `[Guide](docs/)` are resolved to the source README, or
to `index.md` when there is no README. The destination link uses `README.md`:
it must already match, or be included with `include_linked`. The transfer plan
marks a source index mapped to a destination README with `directory_index`.
An ambiguous mapping, or creating a README that would shadow an existing
destination index, is rejected. A queued GitHub bundle rechecks the remote tree
before its exact-head commit. A rejected operation leaves both sources unchanged.
Directory-link transfers upgrade the logical store to v16; an index-to-README
mapping upgrades it to v17. Older binaries refuse these formats, so verify
compatible backups before relying on them.
With that flag, all recursively linked Markdown articles and supported images
are copied atomically into a standalone local destination, or queued together
for one GitHub commit. Cycles are handled;
existing matching articles are retained and conflicts reject the whole batch.
Each newly copied article retains source provenance. Approval for a private-to-public batch covers every article;
each is checked for credential-like text. Independently imported destinations
cannot receive a linked batch. Copy never deletes the source.

For a connected destination, the group is persisted locally in store format v9 or newer,
which older binaries reject. The outbox job is complete only after a complete,
non-withheld pull observes every article and image at the expected digest.
The outbox exposes `observed_commit` only for a completed job; a successful
ref-update response alone does not establish that a subsequent pull observed it.
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
digests, mapped paths, destination status (`missing`, `matching`, or
`conflict`) and a `credential_like` heuristic for each article without exposing
its text. It also includes `source_status` and `destination_status` from the same
authorized snapshot, including partial/dirty flags, observation time, conflicts
and applicable sync status. An observation is not proof of live state. The
heuristic can miss secrets or flag harmless examples. Cycles are
included once; ambiguous path mappings are rejected.
The plan is a read-only snapshot, not a reservation or a guarantee that a
copy or move will succeed. It does not inspect images, embedded HTML,
publication approval, remote write permissions or later changes. It does not
automatically copy missing articles; execution must revalidate all conditions.
Scoped readers must be authorized for both source and destination. Browser
sessions cannot submit this POST; access uses a reader credential or eligible
local reader socket. Copying and moving remain owner-only.

## Restricted local snapshot import

On Linux, the optional `--local-import-source ID` works only with
`--reader-socket`. The kernel must identify the Unix socket peer as UID 0. It
accepts `POST /api/v1/import` without a browser Origin, and only a partial
batch (`complete: false`, and no `withheld_paths` accounting for every withheld
file) for that exact existing, private, disconnected local source, so it never
applies upstream deletions. It cannot create sources, operate GitHub, write
individual articles or change visibility. The same generation and conflict rules apply. Non-root peers
remain read-only; HTTP headers cannot supply peer identity. On other operating
systems this import channel is unavailable. A privileged process can already
access local files; do not treat this mechanism as per-user authorization.

The same root-only Unix socket accepts `POST /api/local/remove-imported-source`
with `expected_generation` and `confirm: true`, to remove the configured
disconnected private local source and its article records. Stop the external
importer first and keep a verified backup. A changed generation, connected
source or retained record depending on the source causes a conflict. The
operation does not delete blobs or remote repositories and is unavailable to
browser and non-root clients. A lost response requires checking current state
before retrying.

## Import observation

Authenticated source listings include `last_import_observed_at` when an import
was successfully accepted since this server process started, including an
unchanged local import or a GitHub branch head freshly verified to match the
imported snapshot. The latter checks stored image blobs but does not re-download
them unless one is missing or corrupt. A failed import
or stale connection observation does not advance it. The value is kept in
memory and is absent after restart until another successful import; it is not
a claim that the upstream checkout or infrastructure is currently correct.
The reader interface shows this time in source warnings; when it is absent or
invalid, it reports the observation time as unknown. It is not a freshness
guarantee, including when an unchanged import refreshed the observation.
`unresolved_conflicts` counts pending conflicts. `last_import_withheld`, when
present for an authenticated reader, reports the importing client's count of
withheld or unavailable observations in the last accepted import attempt.
Combined importers may count the same item in more than one check, so this is
not necessarily a count of unique files or an exhaustive count of paths outside
a curated import's scope. The count is kept in memory and is
absent after restart until another import; a head-only check does not create it.
`import_partial` and `import_dirty` indicate known coverage and source-state limits. These fields
are omitted from public-only listings where applicable. MCP `veda_context`
returns the same status alongside scope instructions. For authenticated access
to a connected source, it also includes `sync_state`, `sync_verification` and,
when present, the sanitized `sync_error` code and `sync_issue`. The warning is
`paused`, `error`, `attention`, `checking` or `unverified` when synchronization
needs attention or visibility verification has expired. An absent warning is
not proof that the upstream repository or infrastructure is current. A failed,
paused or unverified connection can leave the local copy stale even when an
earlier observation time is present. These connection fields are omitted from public context responses
and from local disconnected sources.
The same status includes `unconfirmed_write_jobs` for a connected source: the
number of non-complete, non-superseded outbox jobs at the snapshot. It includes
blocked or ambiguous jobs and is not a count of individual files or moves. Zero
does not establish remote freshness; public context omits the field.
`prepared_unconfirmed_write_jobs` is the subset with a persisted prepared commit
that has not been confirmed or superseded. It can include a successful response
awaiting observation as well as an uncertain response; the count alone cannot
establish what happened remotely. It is omitted for public and disconnected
sources, and does not reveal paths or commit IDs.
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
unsupported. The target must be a new path in another source.
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
conflict resolution and moves of the source, destination
and recorded linked destination articles are refused. External changes in a
GitHub repository cannot be locked by VEDA. A definitive rejection of the
first ref update restores the cancellable waiting state; a later rejection
cannot resolve an earlier uncertain attempt. The two remote repositories
cannot be updated atomically; external edits may occur between checks. Local
source revision history is retained. Private-to-public publication requires
separate explicit confirmation. Without `include_linked`, linked articles are
not copied automatically; missing or different targets must be synchronized first. Live
acceptance with credentials stored by VEDA remains pending.

Creating the first local-source move upgrades the local store format to version 2;
the first GitHub-source move upgrades it to version 3. Older
versions of VEDA reject that store instead of silently discarding move records.
The previous version-2 store reader was tested against a synthetic version-3
file and refused it without changing it. A move containing images to a local target upgrades the store to
version 6; a copy or move with images to a connected target upgrades it to
version 7. Moves with linked articles, and connected copies with linked
articles, upgrade the store to version 8. Older binaries reject these stores.
A synthetic v7 store with a
paused image outbox passed a PBS round-trip; its restored queue then completed
against a local fake GitHub. Production v6/v7/v8 backup and restore are unverified.
Restore a compatible binary or a separately verified backup when rolling back;
do not overwrite newer state with an older server.

The owner-only GitHub connection request accepts `adopt_existing: true` to attach
an existing disconnected private source to a verified private repository. See
[GitHub integration](github.md#attaching-an-existing-private-source) for the
preconditions, conflict behavior, and local-only article handling.

`PUT /api/v1/github/connections` (owner only) reconfigures a connection at its
`expected_config_revision`: `account_id`, `write` and `repository` are each
optional, and at least one is required. `repository` sets the name after a
rename on GitHub; it must denote the same repository identity, otherwise the
request answers 409 (`conflict`). A name GitHub does not know answers 404
(`repository_unavailable`), and the old name of a renamed repository 502
(`repository_moved`). A rename within the same owner is normally followed
automatically; see [GitHub integration](github.md#renamed-repositories).

## Reconcile two private sources

`POST /api/v1/sources/reconcile` (owner only) accepts `source_id`,
`destination_id` and an optional `limit` from 1 to 50 (default 20). It requires
a private source and a fully observed, verified, writable private
GitHub destination of the same kind. Source identities remain separate.
For a connected source, its private visibility and complete imported head must
be recently verified, its connection must be healthy and unpaused, and each
copied revision must match the observed upstream revision. The source and
destination cannot refer to the same repository and branch.
Matching article and referenced-asset observations establish an owner-only
baseline. New articles and safe updates relative to that baseline are queued
using the existing copy/outbox safeguards. Missing images referenced by equal
articles are queued with the article; conflicting images are blocked. Existing
unequal articles without a shared baseline, concurrent edits, pending writes,
unresolved conflicts, unsafe text and unsupported dependencies are reported in
`blocked_paths` and are not overwritten. Nothing is deleted. Small linked
graphs can be queued for a new destination; directory-index mappings and updates
of existing linked graphs may require manual review. A partial source snapshot
cannot establish deletion, and absent paths are retained.

Each call commits bounded individual transactions and may report `more: true`;
repeat until it is false and inspect blocked paths and the destination outbox.
A timeout or interruption may leave earlier transactions committed: check the
state and retry, which is idempotent. Reconciliation persists baselines using
store format v18; older binaries reject this store. Maintain a compatible backup
before invoking it. This endpoint initiates one pass; periodic scheduling is
separately configured as described below.

### Periodic private reconciliation

`GET /api/v1/sources/mirrors` lists owner-only configurations and sanitized status.
`POST` to the same endpoint accepts `source_id`, `destination_id`, `enabled`,
and `expected_revision`: use revision 0 to create a configuration and the
returned revision for later changes. A destination can have one configured
source. Enabled mirror relationships cannot form cycles. Enabling requires the same private-source and verified, writable private
GitHub conditions as manual reconciliation. After each GitHub sync pass VEDA
runs a bounded reconciliation and wakes the worker for additional batches or
queued writes. A changed (including asset-only) or conflicting root-only local import also wakes an
enabled mirror; unchanged imports do not. `ready`, `syncing`, `blocked`, `error`, and `paused` report the
latest pass; `blocked` counts paths requiring review and pending work may be
reported while it is synchronizing. Status is not proof of remote durability:
inspect the GitHub outbox and its observed commit.
The owner-only status includes up to 50 blocked paths; the count may be larger.

The private GitHub administration interface offers the same settings through
its restricted admin session. An interrupted configuration request may have
committed; reload the status before retrying with its current revision.

Pausing stops future reconciliation passes, but does not cancel writes already
queued. Source content is never deleted by this feature. The configuration uses
logical store format v19; older binaries reject it. Back up the data with a
compatible version before enabling. A changed or public repository, an offline
source, errors and conflicting paths stop the affected work rather than making
it public. This applies only after explicitly configuring the source pair.

### Browser state polling

`GET /api/state` returns an `ETag` for the complete response after access
checks. A request with the same `If-None-Match` receives `304` without a body;
any change in the visible response produces a new validator. The
response remains `Cache-Control: no-store`, and clients must discard their
in-memory state when signing out or changing sessions. The browser uses the
validator only within one active page polling cycle.

### Browser knowledge graph

`GET /api/state?graph=1` additionally returns `view.GraphEdges`, a list of
`from` and `to` source-qualified article paths. The graph is derived from
relative Markdown links in the displayed versions of the articles in the same
authorized snapshot used by the page. Directory targets resolve to visible
`README.md` or `index.md`; missing or inaccessible targets, external links,
images, fragments without a path, and duplicate edges are excluded. Links are
resolved inside the originating source, as in article rendering. This endpoint
does not infer semantic relationships or parse unsupported wiki-link syntax.

## Knowledge graph (optional module)

Data model, access rules, operation and a worked example are in
[knowledge-graph.md](knowledge-graph.md). The `/api/v1/knowledge/` routes serve data only when VEDA runs with
`--knowledge`. Without it every read and `reindex` answer
`503 {"error":"knowledge_disabled"}`, `status` answers the owner
`{"enabled":false,"state":"disabled"}`, and every other route is unchanged.
`/api/state` never carries knowledge data.

| Method | Route | Who | Parameters |
| --- | --- | --- | --- |
| GET | `/api/v1/knowledge/entities` | public, reader, owner | `q` (≤200 code points), `source_id`, `type` (schema entity type), `limit` 1–100 (default 50), `offset` 0–10000 |
| GET | `/api/v1/knowledge/entity` | public, reader, owner | `id` (`ent-` + 20 hex), `limit` 1–100 |
| GET | `/api/v1/knowledge/article` | public, reader, owner | `source_id`, `path`, `sha256` (owner only: the publication copy, read beside the owner's current revisions of all other articles), `limit` 1–100 |
| GET | `/api/v1/knowledge/graph` | public, reader, owner | `entity_id` or `source_id`+`path` (+ owner `sha256`), `depth` 1–2 (default 1), `max_nodes` ≤200, `max_edges` ≤400, `sources` and `relations` (comma lists, ≤20 each) |
| GET | `/api/v1/knowledge/context` | public, reader, owner | `q` (1–200 code points), `depth` 1–2, `limit_bytes` 2–16000 (default 8000): the exact JSON size of the returned `items` array, reported as `bytes` |
| GET | `/api/v1/knowledge/status` | owner | none |
| POST | `/api/v1/knowledge/reindex` | owner, not from a browser | JSON body: `scope` (`all`, `source` or `article`), `source_id`, `path`, `force` |

Access follows the computed mode `public := !owner && !reader` over the
request's own snapshot (scoped key, scoped session or team user), checked on
every request: public callers see the published revision of publicly
readable sources, readers and the owner the current revision. Names,
aliases, counts, ranking, candidates, related-article reasons, graph
traversal and context are computed from those snapshots only. The public
listener ignores owner and reader credentials; the reader socket, reader
and scoped keys and browser sessions never reach `status` or `reindex`.
There is no route for Cypher or other free-form queries.

Query strings are strict: an unknown or repeated parameter is
`400 invalid_request`. Errors use the API's flat form `{"error":"<code>"}` with
`invalid_request` (400), `not_found` (404, the same for an unknown and an
unreadable entity, article or SHA), `method_not_allowed` (405),
`reindex_busy` (409), `knowledge_unavailable` (503: the index cannot answer
or the extractor settings are not known yet) and `timeout` (504: an index
query exceeded the read timeout, 10 seconds). `status` and `reindex` are
bounded by the same timeout. A truncated graph or context sets
`truncated: true`; entity pages return `total` and `next`.

`article` returns `status` for the snapshot read: `ready` (with
`knowledge`), `processing`, `stale` (indexed before by an older pipeline or
lost by the index; being rebuilt, nothing old is shown), `unavailable`,
`too_large` or `failed` (the owner also receives the `error` code).
`disabled` is the client-side status for `knowledge_disabled`. Backlog and
job counts appear only in the owner's `status`. `reindex` answers
`202` with `operation_id`; `force` re-extracts instead of rebuilding from
stored results.

MCP exposes these routes through the REST client with the same bounds:
`veda_entities` (`query` → `q`), `veda_entity`, `veda_article_links`
(`article`), `veda_graph` (`entity_id` or `source_id`+`path`, `depth`,
`max_nodes`, `max_edges`) and `veda_graph_context` (`query` → `q`, `depth`,
`limit_bytes`) for readers and the owner; `veda_knowledge_status` and
`veda_knowledge_reindex` for the owner only. MCP does not pass `sha256`,
`sources` or `relations`. A tool answer above 2 MiB is refused.

## Write origin and MCP activity

`veda mcp` labels its requests with `X-Veda-Client: mcp/reader`, `mcp/editor` or
`mcp/owner`. The label is never authorization: a mode is recorded only when the
request's own credential matches it (an editor or owner label needs the owner key,
a reader label a reader credential), and anything else is ignored. A revision the
owner writes through `POST`/`PUT /api/v1/articles` records `via`: `mcp-editor`,
`mcp-owner`, or `rest` for any other client; imported revisions and revisions
written before this field have none. The field is optional in stored revisions: an
older binary reads the store and drops the field from article headers it rewrites,
and a newer binary does not compare `via` between a header and its revision row, so
rolling back and upgrading again keeps the store readable. The last contact of each mode is kept in
memory only and starts empty after a restart; reader keys and the reader socket
share one `reader` entry, and the reader label can be sent by any reader, so it is a
hint, not proof. State views (`/api/state`) carry `MCPLastSeen`, the latest contact
truncated to the minute, only for the owner key or an administrator session.

## Access requests and Telegram notifications

Guests ask for an API/MCP key from the public surface. `POST
/api/public/access-requests` is the only write an anonymous visitor can make: it is
accepted on every listener, only from the same origin, with a body of at most 4 KB.
Sources must be public; the nick must be a Telegram username (`@` and 5–32 of
`A-Z a-z 0-9 _`); the comment is at most 1000 characters without control characters.
A hidden `website` field filled by bots is answered as accepted and dropped. One
address may send 3 requests an hour, all visitors 20 a day, and at most 100 may wait;
otherwise 429. The address is the peer, or `X-Real-IP` when the peer is listed in
`--trusted-proxy`; it is kept in memory only. Requests, users' Telegram IDs and sent
notification marks live in `<data>/notify.sqlite` (0600, own migrations), the bot
token in `<data>/telegram-bot.token` (0600); the main store is unchanged.

The bot only sends (`getMe`, `sendMessage`, plain text without link previews) to
enabled administrators who set a Telegram ID and started the bot. Events, each
switchable: a new access request; a GitHub connection entering an error and recovering
(once each, remembered across restarts); an access key expiring within a day (once per
key and expiry). Notifications are checked every minute; delivery is retried three
times and never blocks a request.

