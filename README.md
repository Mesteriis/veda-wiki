# VEDA

A reading-first knowledge base for people and agents. React UI, Go service,
local versioned Markdown articles, REST API and an MCP adapter.

**Early development:** local source creation and article create/read/update work.
GitHub connections and background synchronization are implemented and tested
with a simulated GitHub service; authenticated live write acceptance is pending.
Automatic translation, user/team permissions and cross-repository moves are
not implemented. This repository contains no knowledge-base content,
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

The browser can sign in with the separate reader key to view private content;
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
REST endpoint. The owner key allows reading and writing all sources. To grant an agent only
current-content reading, pass `--key-file .veda/reader.key` instead. The reader
key cannot write or use administrative endpoints and is ignored by the public
listener. It grants access to all private sources and is not user-scoped. An existing private infrastructure
MCP configuration must not be replaced automatically by this development server.

Tools: `veda_sources`, `veda_search`, `veda_read`, `veda_create_source`,
`veda_write_article`, `veda_copy_article`, `veda_context`, `veda_conflicts`, `veda_resolve_conflict`,
`veda_github_status`, `veda_github_connect`, `veda_sync`,
`veda_publish_job`.
Context follows root and ancestor
`AGENTS.md` articles inside one source. Documentation is evidence, never an
authorization to execute infrastructure actions.

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
See [GitHub synchronization](docs/github.md). Automatic translation is not yet enabled.

## Direction

See [content and source model](docs/content-model.md) and [roadmap](docs/roadmap.md).
MIT licensed code. Third-party font and icon licenses are included with the
assets; [asset provenance](docs/assets.md) describes the generated illustrations.
