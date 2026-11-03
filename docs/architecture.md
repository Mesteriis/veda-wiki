# Backend architecture

The Go backend is one module (`github.com/Mesteriis/veda-wiki`, `go.mod` in the
repository root). `cmd/veda` is the dispatcher; everything else lives under
`internal/`. Boundaries between packages are checked automatically, see
[Guards](#guards).

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/veda` | Entry point: picks the subcommand and calls `internal/app`. |
| `internal/app` | Assembly: server flags, listeners, the reader socket, workers, bounded shutdown and the writer lock. |
| `internal/app/commands` | The non-server subcommands: `createsuperuser`, `access-key`, `migrate-sqlite`, `import`, `github`. |
| `internal/httpapi` | REST handlers, browser sessions, role checks and the embedded reading interface. Adapter only: it parses input, checks the role, calls an area and serializes the result. |
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
python3 scripts/check_file_size.py --max 500 --include 'scripts/**/*.py' --include '**/*.go'
python3 scripts/check_architecture.py
python3 -m unittest discover -s scripts -p test_check_architecture.py -v
```

`check_architecture.py` uses `go list -json ./...` and the source tree. It
prints `path: reason` and exits 1 when:

- `internal/domain` imports `os`, `net`, `net/http`, `database/sql` or `os/exec`;
- an area imports another area outside the table above, a leaf imports an area,
  or an area imports `httpapi` or `app`;
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
