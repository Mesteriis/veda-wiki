# Repository import

The importer observes an explicitly selected **local Git checkout root**. It
does not clone repositories, discover unrelated directories or infer public
visibility from folder names. For remote connections see [GitHub synchronization](github.md).

```sh
./veda import --checkout /path/to/handbook --source handbook --create \
  --key-file .veda/owner.key
```

`--create` creates the source in the same transaction as its first import. Local
sources default to private. `--visibility public` is an explicit owner publication
choice for a local source; it is not proof of a GitHub repository's visibility.
For subsequent imports omit `--create`. `--url` selects a loopback REST endpoint.
The command reads the owner key from its private file without printing it,
disables HTTP proxies and refuses redirects. It uploads through the same REST
service as other writers; it never opens the server database directly.

To inspect the selected tracked checkout before connecting it, run:

```sh
./veda import --checkout /path/to/checkout --preview
```

The command requires Git on the machine where the checkout is inspected.
Preview reports only type, article and image counts, withheld count, completeness
and dirty status. Withheld reasons are aggregate counts for unmerged Git paths,
symlinks, invalid UTF-8, sensitive-content patterns and unsupported images; a pattern match can
be a false positive and is not an exhaustive secret scan. It does not contact
VEDA, read an owner key, create a source,
or list article names or content. It uses the same working-tree selection as a
local import, so untracked files are excluded and `dirty` covers tracked changes
only. It does not compare against an existing VEDA source, predict conflicts or
deletions, verify repository visibility, or preview GitHub synchronization.

For a wiki checkout, optionally compare its eligible articles against a visible
wiki source using a read-only numeric-loopback HTTP endpoint that accepts the
current caller without a credential:

```sh
./veda import --checkout /path/to/checkout --preview --source handbook \
  --compare-url http://127.0.0.1:8080
```

The output adds separate article `comparison` and `asset_comparison` aggregates,
each with `same`, `different`, `checkout_only`, and `veda_only` counts; it does
not include names or contents, send the checkout to VEDA, or
change either side. It uses current article hashes, not import-base revisions,
and the two systems can change during the comparison. Withheld and untracked
files are excluded, so `veda_only` does not imply deletion and `different` does
not establish a conflict. Image counts compare stored source hashes, not blob
availability or historical image bindings. Metadata is not compared. Program sources
are refused because their displayed hashes can refer to translations. The endpoint
must be independently authorized and trusted; the comparison does not read or
send a key, use a proxy, or follow redirects.

## Type detection

A wiki must have a regular root `veda.yaml` file:

```yaml
schema_version: 1
kind: wiki
title: Handbook
description: Documentation for people and agents
image: assets/cover.png
url: https://example.org
```

`description`, `image` and `url` are optional. `image` is a repository-relative
local raster image; `url` is an HTTPS link, never an instruction to fetch it.
The reading interface shows the description and HTTPS link under the source
in its source-specific tree view; the image is used on recent-page cards. Only
sources visible to the reader contribute their metadata.
Unknown fields, duplicate keys, multiple YAML documents, aliases, anchors,
custom tags and unsupported schema versions are refused. The parser uses the
[stable Go YAML v3 API](https://pkg.go.dev/go.yaml.in/yaml/v3), pinned in go.mod.
Metadata cannot set visibility or change repository access.

If `veda.yaml` is absent, the source is a program and only `docs/` is imported.
If `docs/` is absent, the command reports `skipped`; it does not substitute root
README, source code or a `do/` directory. Original program Markdown remains intact. Agents can submit a separate Russian
translation via REST/MCP; see [translation workflow](translation.md). Automatic
translation is not yet configured.

In a program repository, if an entry named `docs` exists but is a file, symlink or submodule instead of
a directory, import fails rather than treating it as an absent directory. A
connected source retains its local snapshot; an unverified public snapshot is
not served while the connection reports `invalid_docs_directory`. Correct the repository entry
and retry synchronization.

## Bounds and provenance

Markdown must be UTF-8 and at most 1 MiB per file. Persisting a document larger
than 256,000 bytes upgrades the store to v13; older binaries reject it. Preserve
and test a compatible backup before rollback. Raster images are
limited to 4 MiB each and MIME-checked (PNG/JPEG/WebP/GIF). Total content is
limited to 32 MiB and 2,000 files; traversal is bounded to 20,000 entries. The
JSON request limit is 64 MiB, including encoding overhead; unusually escaped text
can hit the transport limit first. The JSON storage backend also has its own
64 MiB whole-state limit, including history; use SQLite for larger stores.
Import requests are decoded incrementally and reject duplicate top-level fields
and duplicate file or image paths. Size limits are still enforced during decoding
and validation; provision memory for the data set and database, not just the
request size.
Hidden directories, node_modules, vendor and dist are excluded. Symlinks are
withheld, special files are refused, and os.Root confines filesystem reads.
Only Git-indexed files are eligible: ignored/untracked local Markdown, nested
checkouts and unrelated working directories do not become published content.
Staged new files are eligible and are reported as a dirty observation.
Credential-like Markdown is withheld, never returned in an error message.
Incomplete coverage is explicit and disables automatic upstream deletions. A
public source with incomplete coverage is hidden from public readers until a
complete import succeeds; authorized readers retain access to its local data.
Unmerged Markdown and image paths are withheld even if absent from the working
tree; an unmerged `veda.yaml` refuses the import because the repository type
and metadata cannot be established safely.
For a private source, an incomplete observation also retains previously known
images that were not observed. After a complete observation, missing images
still referenced by a locally changed or local-only article are retained while
the article remains local. An image supplied at the same path by the new import
takes precedence; VEDA does not yet bind images to individual revisions. Old
private images are not retained when a source becomes public.
Before accepting an import, VEDA verifies the images that would remain
referenced, including images retained from earlier partial imports. If one is
missing or corrupt and absent from the incoming batch, the import fails without
advancing its observation or committing article changes. The import API returns
HTTP 503 with `{"error":"asset_unavailable"}` without naming the asset. Restore the missing
blob from a verified backup or provide the image in a subsequent import before
retrying. The service does not invent or silently discard the image.

The source records Git HEAD, dirty working-tree status and a digest of imported
content/metadata. Dirty files can be imported, but this is an observed working
tree, not a claim that every byte belongs to the recorded clean Git commit.
The importer checks HEAD/status before and after reading. It does not execute
repository hooks or Git fsmonitor helpers.

Images are content-addressed immutable blobs written before the metadata
transaction. A failed transaction can leave an unreferenced blob, never a
reference to a partially written image. The media route applies source visibility
and verifies the blob digest before serving. No remote images are fetched.
Blob garbage collection and historical image binding are still future work.
When an import supplies images, VEDA verifies their stored blobs even if the
snapshot is unchanged, and atomically repairs missing or corrupt blobs from the
supplied bytes before reporting the observation as successful. Before accepting
an unchanged GitHub head, VEDA checks current source image blobs; if any are
missing or corrupt it reimports the repository snapshot. Images absent from a
partial import are not restored by that import, and may require separate recovery.
When a retained image remains unavailable after a GitHub reimport, the connection
reports `asset_unavailable`; authorized agent context includes the sync error.
Queued moves and writes are deferred until a later synchronization cycle after
repair, so their dependencies are checked again before any remote mutation.

In the reader, relative Markdown links are resolved within the article's source;
root-relative links start at that source's root. Links that escape the source
root are not redirected to a different connected repository. External links and
in-page fragments retain their normal behavior.
Links to a directory open its visible `README.md`, or `index.md` when no
README is visible. An exact page match takes precedence. Resolution stays
within the selected source and uses the reader's access permissions.
Local Markdown image paths follow the same source-root and confinement rules.

## Concurrency and deletion

Import batches include `expected_import`, the source's previous import generation.
A changed generation causes 409. Repeating an unchanged batch against the current
generation creates no new article revisions or duplicate conflicts.

An upstream change updates a clean article. If the local current text diverges
from the last imported base, both texts remain durable and the article keeps its
local current revision until resolution. A complete upstream removal tombstones
a clean imported article; removal of a locally modified article creates a
deletion conflict. Local-only articles are not deleted by repository absence.

Owner API: `GET /api/v1/conflicts`, `POST /api/v1/conflicts/resolve`. Resolution
supplies source ID, path, conflict index, expected local revision and either
`local` or `upstream`. Stale decisions return 409. Original local revisions and
incoming conflict records remain in history. MCP exposes `veda_conflicts` and
`veda_resolve_conflict` through the same endpoints.
Resolution also returns 409 while a prepared GitHub write or an active linked
bundle for that article remains unresolved; those jobs must not be silently
discarded. See [GitHub synchronization](github.md) for the recovery limitation.
Ordinary article writes are rejected while that article has an unresolved
import conflict, even when its local revision matches. Resolve the conflict
first. A read-only agent can still prepare an unsaved proposal; the proposal
reports conflict status as of preparation, and the write checks again.
The MCP adapter and server must both support the proposal-base endpoint; a
new adapter paired with an older server cannot prepare an update.

Conflict lists contain summaries, ordered by source/path/index, and accept
`limit` (1–100, default 30) and `offset`. To inspect an incoming text, pass
`source_id`, `path`, and `index` to the GET endpoint or `veda_conflicts` tool.
An unchanged incoming version does not create duplicate conflict records; a
newer incoming version supersedes the previous pending decision while retaining
its history. An upstream revert clears the obsolete pending conflict without
discarding the local edit.
