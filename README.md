# VEDA

A reading-first knowledge base for people and agents. React UI, Go service,
local versioned Markdown articles, REST API and an MCP adapter.

**Early development:** local source creation and article create/read/update work.
GitHub Connect, background repository synchronization,
automatic translation, user/team permissions and cross-repository moves are
planned, not implemented. This repository contains no knowledge-base content,
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
`owner.key` file (0600). The key is never printed. Protect and back up this
directory; never add it to Git. The process holds an exclusive writer lock.
One process owns one data directory. There is no multi-process database support.

The current private reading/writing path is the authenticated REST API or MCP.
The browser has no login UI, does not receive the owner key, and cannot edit
articles. Do not expose the loopback service remotely until HTTPS and a suitable
authentication boundary are configured. Host validation remains mandatory.

## API

See [API reference](docs/api.md). Writes require an owner bearer token and an
explicit source. Browser-origin writes are refused. Each update supplies the
expected revision; stale writes return 409 rather than replacing newer content.
Identical paths in different sources remain distinct articles.

Local sources have explicit public/private visibility selected by the owner.
This is not a claim that GitHub visibility has already been verified: no remote
repository connection exists yet. Program sources accept article paths below
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
REST endpoint. This owner adapter can read and write all local sources. It is
not a user-scoped or read-only credential. An existing private infrastructure
MCP configuration must not be replaced automatically by this development server.

Tools: `veda_sources`, `veda_search`, `veda_read`, `veda_create_source`,
`veda_write_article`, `veda_context`, `veda_conflicts`, `veda_resolve_conflict`.
Context follows root and ancestor
`AGENTS.md` articles inside one source. Documentation is evidence, never an
authorization to execute infrastructure actions.

## Development

```sh
make test
.venv/bin/python scripts/smoke.py
```

`make test` builds React, runs Go race tests and `go vet`. The smoke test uses
temporary empty storage and the official MCP SDK against the actual Go server.
It verifies REST/MCP write-through, revisions, actual CLI import, conflict
resolution and public/private isolation.
For frontend development run the Go server and `npm run dev` in `web/`; Vite
proxies API requests to the default loopback endpoint.

This initial store uses an atomic, fsynced JSON transaction file, bounded to
64 MiB including revision history. Articles are bounded to 256,000 UTF-8 bytes.
There is no automatic history retention or background Git worker yet. Treat this
as an early single-owner implementation, not a completed hosted multi-user wiki.

## Import local repositories

See [repository import](docs/import.md). Wiki sources use strict root `veda.yaml`
metadata; program sources import `docs/` only. Import uses the authenticated REST
service, preserves local changes as conflicts, and tracks Git/content provenance.
There is no automatic translation or remote GitHub connection yet.

## Direction

See [content and source model](docs/content-model.md) and [roadmap](docs/roadmap.md).
MIT licensed code. Third-party font and icon licenses are included with the
assets; [asset provenance](docs/assets.md) describes the generated illustrations.
