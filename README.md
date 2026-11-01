# VEDA

A reading-first knowledge base for people and agents. React UI, Go service,
local versioned Markdown articles, REST API and an MCP adapter.

**Early development:** local source creation and article create/read/update work.
GitHub connections and background synchronization are implemented and tested
with a simulated GitHub service. Cross-repository moves, including linked
articles and images, have also passed a synthetic live test through an
authenticated `gh` proxy; VEDA's own saved App/PAT credentials have not passed
live acceptance. An optional local translation worker
produces reviewable Russian drafts; model quality remains unverified. Scoped
agent read keys are implemented; user/team accounts remain unfinished. This
repository contains no knowledge-base content,
credentials, production configuration or deployment history.

## Run

Requires Go 1.26, Node 20.19+ (or 22.12+) and npm. Runtime is a single Go
binary; Node is only needed to build the interface. Linux/macOS are supported.

```sh
make build
./veda
```

Open http://127.0.0.1:8080/. Both listeners show only public content without
authorization. The separate public listener is http://127.0.0.1:8081/ and
**always** ignores owner tokens. The database starts empty.

The first start creates `.veda/` (0700), `state.json` on first write and an
`owner.key` and `reader.key` files (0600). The key is never printed. Protect and back up this
directory; never add it to Git. The process holds an exclusive writer lock.
One process owns one data directory. There is no multi-process database support.
On SIGTERM or SIGINT, VEDA stops accepting requests, cancels background work
and waits up to ten seconds for active requests and workers before exiting.
An incomplete shutdown is logged as an error; durable outbox jobs are recovered
on the next start.

The browser can sign in with the separate reader key or a source-scoped key to view private content;
it cannot edit articles. See [browser access](docs/browser-access.md). Authenticated
REST and MCP remain the writing paths. Do not expose the loopback service remotely until HTTPS and a suitable
authentication boundary are configured. Host validation remains mandatory.

## API

See [API reference](docs/api.md). Writes require an owner bearer token and an
explicit source. Browser-origin writes are refused. Each update supplies the
expected revision; stale writes return 409 rather than replacing newer content.
Identical paths in different sources remain distinct articles.

Local sources have owner-selected visibility. Connected GitHub sources derive
visibility from a verified repository response and fail closed if verification
expires or fails. Program sources accept article paths below
`docs/` only. Wiki sources accept Markdown article paths.

## MCP

Requires Python 3.11+. Install the adapter into a dedicated virtual environment:

```sh
python3 -m venv .venv
.venv/bin/pip install -r mcp/requirements.txt
.venv/bin/python mcp/server.py --key-file .veda/owner.key
```

Register that command with your MCP client, using absolute paths appropriate to
your checkout. stdio is the MCP transport; use `--url` to choose the loopback
REST endpoint. For a local read-only agent, the service can instead expose a Unix socket in a
private directory owned by the service user. Start VEDA with `--reader-socket
/path/to/private/reader.sock`, then use the MCP adapter with `--reader-socket`
pointing to that socket. This adapter advertises sources, search, read, context
the scoped translation queue, and non-persistent preparation of new articles,
updates and translations; it has no write/admin access. The socket's directory must
be 0700 and owned by the server user. Anyone sharing that OS identity can read
all private sources; this is not per-agent authorization. An optional Linux-only
root snapshot import channel is described in the [API reference](docs/api.md).
Translation proposal validation uses a non-mutating POST on this socket; it
checks protected Markdown but does not assess meaning or publish the result.

The owner key allows reading and writing all sources. To grant an agent only
current-content reading, pass `--reader-key-file .veda/reader.key` instead. The reader
key cannot write or use administrative endpoints and is ignored by the public
listener. It grants access to all private sources and is not user-scoped. An existing private infrastructure
MCP configuration must not be replaced automatically by this development server.

Tools: `veda_sources`, `veda_search`, `veda_find`, `veda_read`, `veda_prepare_update`, `veda_prepare_new_article`, `veda_prepare_translation`, `veda_create_source`,
`veda_write_article`, `veda_copy_article`, `veda_transfer_plan`, `veda_moves`, `veda_move_article`,
`veda_cancel_move`,
`veda_translation_needed`,
`veda_translate_article`, `veda_context`, `veda_conflicts`, `veda_resolve_conflict`,
`veda_github_status`, `veda_github_connect`, `veda_sync`,
`veda_publish_job`, `veda_cancel_bundle`.
Context follows root and ancestor
`AGENTS.md` articles inside one source. Documentation is evidence, never an
authorization to execute infrastructure actions.
`veda_prepare_update` rereads the current revision and returns the proposed
text and base digest for review. It does not save a draft or publish anything;
an authorized client must reread or submit with the returned expected revision.
`veda_prepare_new_article` similarly prepares an unsaved creation proposal after
checking that no article is currently visible at the path. It does not reserve
the path; deleted history or a concurrent write can still prevent creation.

## Development

```sh
make test
.venv/bin/python scripts/smoke.py
```

`make test` builds React, tests tree navigation, runs Go race tests and `go vet`. The smoke test uses
temporary empty storage and the official MCP SDK against the actual Go server.
It verifies REST/MCP write-through, revisions, actual CLI import, conflict
resolution and public/private isolation.
For frontend development run the Go server and `npm run dev` in `web/`; Vite
proxies API requests to the default loopback endpoint.

This initial store uses an atomic, fsynced JSON transaction file, bounded to
64 MiB including revision history. Articles are bounded to 256,000 UTF-8 bytes.
There is no automatic history retention yet. Treat this
as an early single-owner implementation, not a completed hosted multi-user wiki.

## Import local repositories

See [repository import](docs/import.md). Wiki sources use strict root `veda.yaml`
metadata; program sources import `docs/` only. Import uses the authenticated REST
service, preserves local changes as conflicts, and tracks Git/content provenance.
See [GitHub synchronization](docs/github.md). The optional translation worker
is disabled by default and needs a separately configured, qualified local model.

## Direction

See [content and source model](docs/content-model.md) and [roadmap](docs/roadmap.md).
MIT licensed code. Third-party font and icon licenses are included with the
assets; [asset provenance](docs/assets.md) describes the generated illustrations.
