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
Unknown fields, duplicate keys, multiple YAML documents, aliases, anchors,
custom tags and unsupported schema versions are refused. The parser uses the
[stable Go YAML v3 API](https://pkg.go.dev/go.yaml.in/yaml/v3), pinned in go.mod.
Metadata cannot set visibility or change repository access.

If `veda.yaml` is absent, the source is a program and only `docs/` is imported.
If `docs/` is absent, the command reports `skipped`; it does not substitute root
README, source code or a `do/` directory. Original program Markdown remains intact. Agents can submit a separate Russian
translation via REST/MCP; see [translation workflow](translation.md). Automatic
translation is not yet configured.

## Bounds and provenance

Markdown must be UTF-8 and at most 1 MiB per file. Persisting a document larger
than 256,000 bytes upgrades the store to v13; older binaries reject it. Preserve
and test a compatible backup before rollback. Raster images are
limited to 4 MiB each and MIME-checked (PNG/JPEG/WebP/GIF). Total content is
limited to 16 MiB and 2,000 files; traversal is bounded to 20,000 entries.
Hidden directories, node_modules, vendor and dist are excluded. Symlinks are
withheld, special files are refused, and os.Root confines filesystem reads.
Only Git-indexed files are eligible: ignored/untracked local Markdown, nested
checkouts and unrelated working directories do not become published content.
Staged new files are eligible and are reported as a dirty observation.
Credential-like Markdown is withheld, never returned in an error message.
Incomplete coverage is explicit and disables automatic upstream deletions.
For a private source, an incomplete observation also retains previously known
images that were not observed. After a complete observation, missing images
still referenced by a locally changed or local-only article are retained while
the article remains local. An image supplied at the same path by the new import
takes precedence; VEDA does not yet bind images to individual revisions. Old
private images are not retained when a source becomes public.

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

In the reader, relative Markdown links are resolved within the article's source;
root-relative links start at that source's root. Links that escape the source
root are not redirected to a different connected repository. External links and
in-page fragments retain their normal behavior.
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

Conflict lists contain summaries, ordered by source/path/index, and accept
`limit` (1–100, default 30) and `offset`. To inspect an incoming text, pass
`source_id`, `path`, and `index` to the GET endpoint or `veda_conflicts` tool.
An unchanged incoming version does not create duplicate conflict records; a
newer incoming version supersedes the previous pending decision while retaining
its history. An upstream revert clears the obsolete pending conflict without
discarding the local edit.
