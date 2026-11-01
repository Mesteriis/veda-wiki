# REST API v1

JSON responses. Owner requests use `Authorization: Bearer <owner-key>`.
The default listener binds loopback. The key is generated into the private
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

Source body:

```json
{"id":"handbook","title":"Handbook","kind":"wiki","visibility":"private"}
```

`id` is a lowercase alphanumeric/hyphen/underscore slug, at most 64 characters.
`kind`: `wiki` or `program`. `visibility`: `public` or `private`. Source visibility
and identity are immutable in this release. Source creation is an explicit local
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
trailing JSON are rejected. This release has no deletion or move endpoint.

Program article paths must start with `docs/`. Absolute paths, traversal, hidden
components, backslashes and non-Markdown paths are refused. Paths are logical
keys inside the store, never arbitrary filesystem paths. Rendered Markdown
disables raw HTML and dangerous link schemes. There is no CORS write interface.

For retries, reread after an ambiguous response: an accepted write might already
be durable. The revision precondition prevents duplicate writes or stale updates.
This is optimistic concurrency, not an idempotency-key implementation. Unknown
source IDs cannot create implicit sources. No write is pushed to GitHub yet.
