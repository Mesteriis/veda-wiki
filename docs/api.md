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
trailing JSON are rejected. Copying retains the original. There is no deletion or move endpoint yet.

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
