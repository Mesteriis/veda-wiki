# Architecture

The Go backend is one module (`github.com/Mesteriis/veda-wiki`, `go.mod` in the
repository root). `cmd/veda` is the dispatcher; everything else lives under
`internal/`. Boundaries between packages are checked automatically, see
[Guards](#guards).

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/veda` | Entry point: picks the subcommand and calls `internal/app`. |
| `internal/app` | Assembly: server flags, listeners, the reader socket, workers, bounded shutdown and the writer lock. |
| `internal/app/commands` | The non-server subcommands: `createsuperuser`, `access-key`, `migrate-sqlite`, `migrate-revisions`, `import`, `github`, `mcp`, `mcp-relay`. |
| `internal/httpapi` | REST handlers, browser sessions, role checks and the embedded reading interface. Adapter only: it parses input, checks the role, calls an area and serializes the result. |
| `internal/mcp` | MCP server behind `veda mcp`: the tool table (13 read-only tools for reader keys and the socket), strict argument validation against each tool's schema before any request, and handlers that call the REST service. Adapter only. |
| `internal/mcp/client` | REST client of the MCP adapter. Adapter only: loopback HTTP or explicitly allowed reader-only HTTPS, private key and socket files, no proxy or redirects, 24 MiB response limit, sanitized refusal messages. |
| `internal/auth` | Access keys, named users and teams, password hashing and the login limiter, browser sessions. |
| `internal/importer` | `veda.yaml` metadata, bounded local checkout observations, batch decoding, asset storage, import conflicts and the process-local observations. |
| `internal/transfer` | Copies and moves between sources, linked graphs and images, and the transfer plan. |
| `internal/translation` | Validation and storage of Russian translations, the list of articles that need one, and the optional local translation worker. |
| `internal/syncer` | Background synchronization with GitHub: connections and accounts, pulls, the write outbox, publication approval, source mirrors and reconciliation. |
| `internal/agentctx` | What agents see before they read or propose: root and ancestor `AGENTS.md` instructions, source status and proposal bases. |
| `internal/store` | The data directory: one lock over the in-memory database and durable commits to the JSON or SQLite backend, migrations and scoped views. |
| `internal/domain` | The data model, errors, validators and the pure state-transition rules over a `Database`. No I/O, no clock reads. |
| `internal/github` | GitHub API client. It contains no synchronization logic. |
| `internal/render` | Markdown rendering, cross-source links, table of contents and the link graph. |
| `internal/search` | Lexical search. |
| `internal/webui` | The built reading interface, embedded with `embed`. |

Test helpers are separate packages: `internal/store/storetest` builds stores
with data and `internal/github/githubtest` is an in-memory GitHub server.

## Dependency direction

```
cmd -> app -> httpapi -> areas -> store -> domain
```

The areas are `auth`, `importer`, `transfer`, `translation`, `syncer` and
`agentctx`. `domain`, `render`, `search`, `github`, `store` and `webui` are
leaves with respect to areas: they never import one. `httpapi`, `app` and
`app/commands` are adapters and assembly and may import anything. An area never
imports `httpapi` or `app`.

An area imports another area only through this list:

| Importer | Imports | Reason |
| --- | --- | --- |
| `syncer` | `importer` | Applies pulled batches. |
| `syncer` | `transfer` | Moves deletion transitions. |
| `agentctx` | `importer` | Reads import observations for source status. |

Logic that two areas need belongs in `domain` as a pure function.

## Collection ownership

The collections below have one owner that may write them. Everyone else reads
them through `store.View` or the owner's API. These are the collections the
guard checks.

| Collection | Owner |
| --- | --- |
| `Outbox`, `Connections`, `Accounts`, `SourceMirrors` | `syncer` |
| `Moves` | `transfer` |
| `AccessKeys`, `Users`, `Teams` | `auth` |

`internal/domain` and `internal/store` may write any collection, because the
pure rules and the commit machinery live there. Cross-cutting rules needed
when an article is written (queue a push, reconcile moves, check visibility)
are `domain` functions that the writing operation calls.

Revision rules are pure `domain` functions: `revisions.go` (numbering and
lookup by number), `prune.go` (the three-newest rule and the protected
revisions), `revision_assets.go` (the images a revision binds) and
`blobrefs.go` (every blob a database references). `store` applies them: it
prunes in every commit, writes the `revisions` and `revision_assets` rows
together with the article, and collects unreferenced blobs under the write
lock. `veda migrate-revisions` also lives in `store`; `app/commands` only
registers it.

`Sources` and `Articles` are shared content, not owned by one package. The
article and source operations in `importer`, `transfer` and `translation` (and
`httpapi` through `domain` rules) write them directly and use `domain`
functions for the invariants they share. They are not ownership-checked.

## Transaction model

`store.Store` holds the database under one lock.

- `View(func(*domain.Database) error)` reads under the read lock. The callback
  must not modify the database or keep its maps, slices or pointers.
- `Update(func(*Tx) error)` takes the write lock and gives the callback a clone
  through `tx.DB()`. If the callback returns an error, neither memory nor disk
  changes. Otherwise the clone is committed.
- `tx.OnlyArticle(key)` declares that the transaction changes exactly one
  article (plus outbox jobs and move states for it) and removes nothing, so
  SQLite writes only those records. `tx.OnlyConnections()` does the same for
  GitHub connection records. Conflicting declarations fall back to persisting
  the whole state.
- `tx.AfterCommit(fn)` runs `fn` under the write lock after a successful commit
  and never after a failure.
- `Exclusive(func(*Exclusive) error)` holds the write lock across several
  transactions, for operations that must serialize a remote update with the
  local state it was prepared from. Each `ex.Update` inside it is its own
  transaction and stays committed even if the callback later fails.

A failed SQLite commit reloads the committed records or, if that is impossible,
fails the store closed.

## Guards

`make lint` runs these checks; CI runs their unit tests.

```sh
python3 scripts/check_file_size.py --max 500 --include 'scripts/**/*.py' --include '**/*.go' \
  --include 'web/src/**/*.ts' --include 'web/src/**/*.tsx' --include 'web/src/**/*.css'
python3 scripts/check_architecture.py
python3 -m unittest discover -s scripts -p test_check_architecture.py -v
```

`check_architecture.py` uses `go list -json ./...` and the source tree. It
prints `path: reason` and exits 1 when:

- `internal/domain` imports `os`, `net`, `net/http`, `database/sql` or `os/exec`;
- an area imports another area outside the table above, a leaf imports an area,
  or an area imports `httpapi`, `app` or `mcp`;
- an `internal/<pkg>` is in none of the area, leaf or adapter sets, so it would
  otherwise escape the checks (subpackages such as `store/storetest` and
  `github/githubtest` count with their parent);
- a non-test Go file assigns to or deletes from a checked collection outside its owner,
  `internal/domain` and `internal/store` (test files and `internal/store/storetest`
  are not checked).

Import checks look at non-test imports. The ownership check is a line-based
text match: it catches direct writes such as `db.Moves[k] = v`,
`delete(db.Users, k)` and `db.Users = ...`, not writes through an alias.
The allow-lists are in the script and change together with this document.

## Frontend

React is a reading interface in TypeScript under `web/src`; it holds no article
logic that the Go service does not own.

- `api/` is the typed REST client and the only place that calls `fetch`
  (`api/http.ts`).
- `lib/` holds pure logic without React or network access.
- `hooks/` holds shared React hooks.
- `components/` is the application shell (layout, sidebar, header) and its CSS.
- `features/<area>/` holds one screen area (reading, catalog, home, session,
  graph, recent, github, access) with its stylesheet beside the components.
- `styles/` holds design tokens, fonts and base rules.

Dependencies point one way: features may import components, hooks and lib, but
components never import features. `App.tsx` is the composition root that hands
feature bodies (profile, GitHub, access settings) to the shell's dialog.

CSS is imported centrally from `main.tsx` in cascade order: fonts, tokens,
base, shell, then features. Importing a stylesheet from a component would place
it before the shell CSS in the bundle and change which rule wins.

`tsconfig.json` sets `strict`, `noImplicitAny` and `noUncheckedIndexedAccess`;
JavaScript is not allowed, and ESLint forbids `any`
(`@typescript-eslint/no-explicit-any`). `make lint` fails any `.ts`, `.tsx` or
`.css` file in `web/src` over 500 lines (the Guards command above).
