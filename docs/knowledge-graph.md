# Knowledge graph (optional module)

VEDA can extract entities and typed relations from the articles it already
holds, keep them as a derived graph in Neo4j and serve them, with evidence,
over REST and MCP. The module is **off by default**. Without `--knowledge`
VEDA opens nothing new, needs no Python, model weights, Docker or Neo4j, and
its routes, MCP tools and interface behave as before (the knowledge routes
answer `knowledge_disabled`).

Everything the module produces is a hypothesis derived from article text.
It never rewrites Markdown or GitHub repositories, and it never decides who
may read what: access is computed by VEDA on every request.

Not implemented and not claimed: PDF, DOCX or OCR input, answer generation or
chat, a vector database, Microsoft GraphRAG (neighbour traversal here is not
GraphRAG), user-defined schemas through the API, editing the graph, and
merging entities by name similarity.

## Components

| Part | Where | Role |
| --- | --- | --- |
| Contracts, preparation, access, resolution, worker, read service | `internal/knowledge` | Go; imports only `domain` |
| Manifest, jobs, immutable extraction results | `internal/knowledge/jobs` → `<data>/knowledge.sqlite` | Go, SQLite (modernc), 0600 |
| Extractor HTTP client | `internal/knowledge/extractorclient` | Go |
| Graph index | `internal/knowledge/neo4jindex` (Neo4j Community, driver v6.4.0) | Go; derived, rebuildable |
| Reference index | `internal/knowledge/memindex` | Go; tests and `--knowledge-index=memory` |
| Shared index contract tests | `internal/knowledge/indextest` | Go tests |
| REST and MCP adapters | `internal/httpapi/knowledge.go`, `internal/mcp/*knowledge*.go` | Go |
| Extractor | `services/knowledge-extractor` | Python 3.12, `gliner2==2.1.0`, Starlette, one process |
| Development profile | `deploy/knowledge/compose.yaml`, `scripts/knowledge_stack.py` | Neo4j + CPU extractor |

The extractor only receives typed text blocks from Go and returns spans. It
never reads files, paths, URLs or Git and keeps no state between requests.
Private articles are never sent to an external AI provider: the model runs
locally from a pinned checkpoint.

## Data model

- **Snapshot**: one immutable text of an article, identified by
  `(source_id, path, content_sha256, pipeline_fingerprint)`. Revision numbers
  are never used as identity. An article has up to two active snapshots: its
  current revision and, for a GitHub-connected source, its publication copy
  when that has a different SHA.
- **Block**: a heading, paragraph, list item or table cell produced by
  `knowledge.Prepare`, with a source map to the Markdown. Frontmatter, fenced
  and indented code, inline code, HTML, images and link destinations are not
  extracted; link text and autolink labels are. Block IDs are
  `b<ordinal>-<8 hex>`.
- **Mention**: one span the model typed as an entity, with its raw score and
  evidence. Model-local IDs (`e1`) never leave the extraction result.
- **Entity**: the canonical node a mention resolves to (`ent-` + 20 hex of the
  canonical key) with a `resolution` method (below). Its displayed name,
  aliases and counts are recomputed per request from the readable mentions.
- **Claim**: one typed relation between two entities in one snapshot
  (`clm-…`), never collapsed into a single edge between global entities, so
  independent, repeated and contradicting statements stay separate. Status
  `extracted`, or `uncertain` with `markers` (below).
- **Evidence**: `source_id`, `path`, `content_sha256`,
  `pipeline_fingerprint`, `block_id`, `quote`, block offsets `start`/`end`
  and Markdown offsets `source_start`/`source_end`, and `exact`.

All offsets are half-open ranges of **Unicode code points** in Go, Python,
JSON and the interface. `quote == markdown[source_start:source_end]` (in code
points) when `exact` is true; otherwise the quote is the block excerpt and
must not be highlighted in the source.

In Neo4j: `(:Snapshot {id, key, source_id, path, sha, fp, generation, complete})`,
`(:Snapshot)-[:HAS]->(:Evidence)`, `(:Mention)-[:AT]->(:Evidence)`,
`(:Mention)-[:OF]->(:Entity)`, `(:Claim)-[:AT]->(:Evidence)`,
`(:Claim)-[:HEAD|TAIL]->(:Entity)`. A snapshot is published in one write
transaction that sets `complete = true` last; reads only start from complete
snapshots. Five uniqueness constraints and a range index, all available in
Community, are created on first use.

### Pipeline fingerprint

`kg1-` + SHA-256 over the schema digest, `prepare-v2`, the resolution version
(`normalize-v3+markers-v2`, plus the alias file digest) and the checkpoint
`fastino/gliner2.5-multi-v1@cf5593a5…` plus a digest of the extractor's
reported `model.pipeline` (chunker version, window 512 / margin 16 / overlap
64 tokens, per-block caps, JointIE decoder settings, library version). Any
change produces a new generation: the old one is retired, reads use only the
active fingerprint, and articles show `processing` until reprocessed.
**The device (`cpu`, `mps`, `cuda`) is not part of the fingerprint.** Switching
devices therefore reuses results computed on another device. On the
evaluation set below CPU and MPS gave identical entities, relations and
scores to two decimals, which is evidence, not a guarantee; use
`reindex` with `force` after a device change if exact provenance matters.

### Statuses

| Level | Values |
| --- | --- |
| Job (manifest row) | `pending`, `running`, `succeeded`, `failed`, `superseded` |
| Module (`status.state`) | `ready`, `processing` (work pending or extractor settings not known yet), `degraded` (with `reasons`) |
| Article (`article.status`) | `ready`, `processing`, `stale` (indexed under an older pipeline or lost by the index; nothing old is shown), `unavailable`, `too_large`, `failed`; `disabled` is the client mapping of `knowledge_disabled` |
| Extractor chunk | `ok`, `truncated_prevented` (cut at a word: nothing dropped), `infeasible` (JointIE `feasible=False`; not "nothing found") |

Failure codes in the manifest and `status.reasons`: `too_large`,
`extractor_rejected`, `invalid_result`, `timeout`, `extractor_error`,
`store_unavailable`, `model_mismatch`, `extractor_unavailable`,
`index_unavailable`, `manifest_unavailable`, `extractor_reconfigured`.
Retries use exponential backoff from 30 s up to 30 min, at most 6 attempts;
outages (extractor or index down, reconfiguration, shutdown) refund the
attempt instead of spending it. An extractor answer `503 busy` (its single
inference slot stayed taken for the whole request time) is not an outage:
the job is retried after 5 s with the attempt refunded, no failure code and
no `degraded` state.

## Access and SHA selection

For every request VEDA builds the allowed set of `(source_id, path, sha)` from
the request's own store snapshot with the computed mode
`public := !owner && !reader`, using `domain.ReadSnapshot`,
`domain.PublicRevision` and `domain.SourcePublicReadable`. The index receives
that set and filters evidence **before** traversal or aggregation; names,
aliases, counts, ranking, candidates, related-article reasons and context are
computed from it only.

| Caller | Snapshot read |
| --- | --- |
| Public (anonymous, public listener with any credential, expired or revoked key) | the publication copy of publicly readable sources: for local sources the latest revision, for GitHub sources `Article.Published` while the verification is fresh, the import is complete and the head imported |
| Reader key, reader socket, reader session | current revisions of every source |
| Scoped key, scoped session, team user | current revisions of the granted sources |
| Owner | current revisions; `sha256=` on `article` and article-centred `graph` reads the publication copy instead |

Unknown and unreadable IDs, paths and SHAs give the same `404 not_found`.
Expiry, revocation, a visibility change, a partial import or an expired
GitHub verification take effect on the next request without waiting for
index cleanup. Deleting or moving an article removes it from answers
immediately and retires its snapshots; a shared entity stays while another
readable snapshot mentions it. Neo4j and the extractor are never reachable
from the browser and there is no route for Cypher or free-form queries.

## Extraction

The extractor runs GLiNER2.5 (`fastino/gliner2.5-multi-v1`, revision
`cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3`, mDeBERTa-v3-base, Apache-2.0)
through `gliner2.joint_ie.JointIE` with an explicit schema. JointIE was
chosen on the real model: plain `extract` with entities and relations
returned relation heads that disagreed with the entity spans
(`"Сервис billing"` against the entity `"billing"`) and does not enforce
endpoint types, while JointIE returns consistent local IDs with typed
endpoints. Beam decoding is kept; greedy was 8× faster on 8 samples but added
wrong `part_of` relations on 2 of them.

Schema v1 (`internal/knowledge/schema/v1.json`): entity types `person`,
`organization`, `project`, `service`, `host`, `repository`, `technology`,
`document`; relations with allowed ends:

| Relation | Head | Tail |
| --- | --- | --- |
| `depends_on` | service, project | service, technology, repository |
| `runs_on` | service | host |
| `uses` | project, service, person | technology, service |
| `part_of` | service, project, host, repository | project, organization |
| `maintained_by` | project, service, repository | person, organization |
| `documents` | document | project, service, host, repository, technology |

gliner2 2.1.0 passes entity descriptions to the model but not relation
descriptions. Long blocks are cut by the extractor into windows of 496
tokens including the schema prompt (184 tokens), with 64 tokens of overlap,
at sentence ends where possible; spans are mapped back to block code points,
deduplicated by `(type, start, end)` and checked against the block text; the
tail is always covered. A relation is only produced when both ends occur in
one chunk; relations across documents are built by Go from mentions.

**Scores** (`score`, `head_score`, `tail_score`) are raw model scores in
[0, 1]. They are not probabilities that a statement is true and are not
calibrated; use them only to sort or to drop obvious noise.

**Uncertain claims.** Go marks a claim `uncertain` when the sentence holding
both ends contains a versioned ru/en marker of negation (`not`, `no longer`,
`не`, `больше не` …), plan (`will`, `TODO`, `будет` …), example (`for
example`, `such as`, `например` …) or past (`was`, `previously`, `раньше`,
`был` …), and lists them as `category:word`. Nothing else is classified. The
rule is per sentence: in "X runs on A but does not use B" both claims are
uncertain.

### Entity resolution

1. An explicit alias from `<data>/knowledge-aliases.json`
   (`{"version":1,"aliases":{"service":{"pg":"postgres"}}}`) → `alias`.
2. A technical key for `host`, `repository` and `service` (a leading type
   word `host`, `server`, `service`, `repo`, `repository`, `хост`, `сервер`,
   `сервис` or `репозиторий` followed by white space is dropped, so
   "host db-01" and "db-01" share the key `db-01`; then lower case,
   spaces removed, only letters, digits and `.-_/:@`) → `technical_key`; it
   merges across sources. The displayed name keeps the text as extracted.
3. Any other name is scoped to its source → `same_source_name`. Equal names
   in different sources (persons, technologies, projects …) stay separate
   entities and are offered as `candidates`, never merged.

Mixed scripts are never normalized together (`Cервер-1` with a Latin `C` and
`Сервер-1` are different). Entities mentioned in more than 25% of the readable
documents (and in at least 5) are treated as generic and do not make articles
"related". The alias file is user configuration: it is not part of the index
and is never deleted by a reindex; a change re-resolves every snapshot
without re-extracting.

## API and MCP

REST (details and bounds in [docs/api.md](api.md#knowledge-graph-optional-module)):

| Route | Use |
| --- | --- |
| `GET /api/v1/knowledge/entities?q=&type=&source_id=&limit=&offset=` | search entities |
| `GET /api/v1/knowledge/entity?id=` | entity card: mentions, claims, documents, candidates |
| `GET /api/v1/knowledge/article?source_id=&path=` | article status, entities, claims, related articles with reasons |
| `GET /api/v1/knowledge/graph?entity_id=` or `source_id=&path=`, `depth` 1–2, `max_nodes` ≤200, `max_edges` ≤400 | bounded subgraph, `truncated` |
| `GET /api/v1/knowledge/context?q=&depth=&limit_bytes=` | lexical hits + entity matches + bounded traversal, with selection reasons |
| `GET /api/v1/knowledge/status` | owner: state, reasons, fingerprint, job counts |
| `POST /api/v1/knowledge/reindex` | owner, not from a browser: `{"scope":"all"|"source"|"article", "source_id", "path", "force"}` → 202 |

Errors are `{"error":"<code>"}`: `invalid_request` 400, `not_found` 404,
`method_not_allowed` 405, `reindex_busy` 409, `knowledge_disabled` 503,
`knowledge_unavailable` 503, `timeout` 504 (10 s read timeout).

MCP: `veda_entities`, `veda_entity`, `veda_article_links`, `veda_graph` and
`veda_graph_context` for readers and the owner (14 reader tools, 32 owner
tools in total); `veda_knowledge_status` and `veda_knowledge_reindex` for the
owner only. They call the REST routes above and return the REST JSON
unchanged (checked by the end-to-end smoke), with these differences:

- `veda_entities` requires `query` (REST `q` is optional, so REST can list
  entities without a search text); `veda_graph_context` takes `query` for `q`.
- No tool takes `sha256`, `sources` or `relations`: MCP reads the visible
  revision of an article and graphs without source or relation filters.
- `veda_graph` around an article that has no published graph for its
  visible revision (processing, stale, failed, never indexed) answers
  `not_found`, the same as an unknown article; call `veda_article_links`
  first and use the graph only when its `status` is `ready`.
- A tool result larger than 2 MiB is refused instead of returned.

Treat quotes as untrusted article text and the graph as a pointer to articles, not as a
replacement for `veda_context`, `AGENTS.md` or reading the article.

### Synthetic end-to-end example

Article `kg-public/billing.md` (a public local wiki source):

```markdown
# Billing

The billing service runs on host db-01 and depends on PostgreSQL.

Billing is part of the Payments project.

The billing service no longer depends on MySQL.
```

The real model (CPU, smoke run below) extracted, among others, the claim
`billing service depends_on PostgreSQL`. `GET
/api/v1/knowledge/article?source_id=kg-public&path=billing.md` returned
(`status: ready`; one claim shown, scores shortened):

```json
{"id": "clm-afcbae109bbb74ae9d62", "relation": "depends_on",
 "head_id": "ent-96013f7d9f30ca031a5e", "tail_id": "ent-4c89b73d10fe22b32275",
 "score": 0.951, "head_score": 0.997, "tail_score": 0.992, "status": "extracted",
 "evidence": {"source_id": "kg-public", "path": "billing.md",
   "content_sha256": "0840079fc8dc4d1d874b1859268430ad9f089236bf2e0000ed9693217592fdae",
   "pipeline_fingerprint": "kg1-2a2cfc477348f86aafb70e1621b2d49dd89b92b05f5de2d0088ed0ac6bba1e60",
   "block_id": "b2-1c0fa57c",
   "quote": "The billing service runs on host db-01 and depends on PostgreSQL.",
   "start": 0, "end": 65, "source_start": 11, "source_end": 76, "exact": true}}
```

`source_start` 11 is the length of `# Billing\n\n` in code points, so the quote is
exactly `markdown[11:76]`. The entities of that answer were `db-01` (host,
`technical_key`, 3 documents for the owner, 2 publicly), `billing service`
(service, key `service:billingservice`), `PostgreSQL`, `MySQL` and
`Payments project` (`same_source_name`). The last paragraph gave
`depends_on billing service → MySQL` with `"status": "uncertain",
"markers": ["negation:no longer"]`. The model also returned `uses` for both
pairs (see Evaluation). The first related article was `kg-private/vault.md`
(owner only) with reasons `shared_entity` (db-01) and two `relation`
(`runs_on`) reasons. `veda_article_links` with the same arguments returned
the identical JSON as its text content.

## Interface

> **To be completed after Task 7 is merged** (the UI is developed on a
> separate branch; no screenshots are claimed here). Planned per the plan:
> a "Links" / "Knowledge graph" switch on the existing graph screen
> (`?graph=1&mode=knowledge`), entity search, source and relation filters,
> a centre node, zoom, an element card with documents, explanation, quote
> and a link to the article, an "Entities" and "Related articles" block in
> the article view, a text list of relations, keyboard navigation and the
> states disabled / processing / stale / unavailable / empty / loading. All
> of it reads only; quotes are rendered as text.

## Running

### Plain VEDA

Unchanged: `make build && ./veda`. No knowledge flag, nothing new is opened.

### Knowledge profile (development)

```sh
# once, with network: build the extractor image and fetch the pinned model
# (1167 MB incl. README images) into the model-cache volume
python3 scripts/knowledge_stack.py up --extractor --fetch-model
# later starts are offline
python3 scripts/knowledge_stack.py up --extractor
python3 scripts/knowledge_stack.py status
```

`up` creates `deploy/knowledge/.secrets/` (0700; `neo4j_password`,
`neo4j_auth`, `extractor.key`, each 0600, git-ignored, never overwritten) and
prints the VEDA flags with file paths only:

```sh
./veda --knowledge --knowledge-index=neo4j \
  --knowledge-neo4j-uri=bolt://127.0.0.1:7687 --knowledge-neo4j-user=neo4j \
  --knowledge-neo4j-password-file="$PWD/deploy/knowledge/.secrets/neo4j_password" \
  --knowledge-extractor-url=http://127.0.0.1:8790 \
  --knowledge-extractor-key-file="$PWD/deploy/knowledge/.secrets/extractor.key"
```

| Flag | Default | Notes |
| --- | --- | --- |
| `--knowledge` | off | other `--knowledge-*` flags without it are an error |
| `--knowledge-extractor-url` | | `http://` only on `127.0.0.1`, `[::1]` or `localhost` with a port; otherwise `https://` |
| `--knowledge-extractor-key-file` | | absolute path, regular file, mode without group/other bits, ≤4 KiB |
| `--knowledge-index` | `neo4j` | `memory`: development only, not persistent (rebuilt from stored results on start) |
| `--knowledge-neo4j-uri` | | `bolt`/`neo4j` on a numeric loopback address with a port, or `bolt+s`/`neo4j+s` |
| `--knowledge-neo4j-user`, `--knowledge-neo4j-password-file` | | same file rules as the key |
| `--knowledge-workers` | 1 | 1–4; the extractor runs one inference at a time (`inference_concurrency` must be 1), so more than 1 worker adds no extraction throughput: extra requests wait for the slot or come back `503 busy` and are retried |
| `--knowledge-reconcile-interval` | 5m | 30s–24h |

Schema and limits are versioned in the repository, not flags. The owner
token is never sent to the extractor; logs carry reason codes only.

### Native extractor (macOS, CPU or MPS)

```sh
uv run --project services/knowledge-extractor python scripts/fetch_knowledge_model.py   # once, network
cd services/knowledge-extractor
HF_HUB_OFFLINE=1 uv run knowledge-extractor --key-file ~/.config/veda/extractor.key [--device mps]
```

The service sets `HF_HUB_OFFLINE=1` and `TRANSFORMERS_OFFLINE=1` itself
unless set explicitly, resolves the pinned snapshot with
`local_files_only=True`, and without a cache answers `/ready` 503 with the
fetch command. An unavailable device is a `/ready` error; there is no
silent fallback to CPU. The Docker image is CPU-only (Linux torch CPU
wheels); Docker never uses MPS.

### Offline operation

After the one-time fetch nothing needs the network. In the compose profile
Neo4j and the extractor are only on the internal `backend` network (no
egress); two `alpine/socat` forwarders publish Bolt and the extractor on
`127.0.0.1` because Docker does not publish ports of containers on an
internal network. `fetch-model` is the only service with internet access.

## Operations

- **Enabling** on an existing data directory: start with the flags. Reconciliation
  queues every active snapshot; the module is `processing` until done.
  Article writes, imports and reads never wait for it.
- **Reindexing**: `POST /api/v1/knowledge/reindex` or `veda_knowledge_reindex`
  with scope `article` (`source_id`+`path`), `source` (`source_id`) or `all`.
  One operation at a time (`409 reindex_busy`). Without `force` graphs are
  rebuilt from the stored extraction results (no model call); with `force`
  the stored results of those snapshots are deleted and the extractor runs
  again. The call only marks manifest rows; workers do the work.
- **Changing the pipeline** (schema, preparation, markers, aliases, model or
  extractor settings) changes the fingerprint: everything is reprocessed
  automatically. An alias-only change reuses stored extraction results.
- **Disabling**: restart without `--knowledge`. Nothing is read or written
  by the module; the derived data stays until removed.
- **Removing derived data**: stop VEDA, delete `<data>/knowledge.sqlite`
  and the Neo4j data (`docker volume rm veda-knowledge_neo4j-data` with the
  stack down). Keep `<data>/knowledge-aliases.json`, which is user
  configuration. `knowledge_stack.py down --volumes` also deletes the model
  cache. With only Neo4j wiped, VEDA republishes from stored results; with
  only `knowledge.sqlite` deleted, it re-extracts everything.
- **Migrations**: `knowledge.sqlite` has its own `migrations` table (v1:
  `manifest`, `results`; v2: `meta` with the last extractor settings).
  Migrations run on start; a database newer than the binary is refused
  ("knowledge database version N is newer than this VEDA"). It is a derived
  cache: when in doubt, delete it rather than downgrade.
- **Diagnostics**: owner `GET /api/v1/knowledge/status` (state, reasons,
  fingerprint, job counts, `dropped` counters since start, last reindex);
  `article.status` per article; `python3 scripts/knowledge_stack.py status`;
  extractor `GET /health` (process) and `GET /ready` (model loaded and a
  self-test inference passed, with the pipeline settings).
- **One writer per Neo4j database.** Run one VEDA per stack; two VEDA
  processes publishing into one database are not supported.
- **Neo4j password**: the image sets it only on the first start of an empty
  data volume. Changing `neo4j_password` later needs `ALTER USER` or
  `down --volumes`. The entrypoint reads `NEO4J_AUTH_FILE` only when the
  `neo4j` user owns it, so the one-shot `neo4j-secret` service copies the
  secret into a volume as `neo4j:neo4j 0400`.
- **Neo4j contract tests wipe data**: `go test ./internal/knowledge/neo4jindex`
  with `VEDA_TEST_NEO4J_URI` deletes every VEDA-labelled node before and
  after each case. Point it only at a disposable database.
- **CPU vs MPS**: use CPU. On this Mac MPS loads but is slower (below).
- **Reads scale with the readable corpus**: aggregates are computed per
  request over every allowed mention (measured below).

## Verification

`make test` covers the module without model, Docker or network (fake
extractor, memindex, contract suite). Real checks are separate commands:

| Command | Needs |
| --- | --- |
| `make extractor-test` | uv; fake model |
| `make extractor-real` | the fetched model |
| `VEDA_TEST_NEO4J_URI=… go test ./internal/knowledge/neo4jindex` | a disposable Neo4j |
| `<python with mcp> scripts/smoke.py` | official MCP SDK; fake extractor, memory index |
| `<python with mcp> scripts/smoke_knowledge.py [--bulk N]` | Docker, the model cache volume; real model and Neo4j |
| `python3 services/knowledge-extractor/eval/evaluate.py --url … --key-file …` | a running extractor |
| `go test -run TestMarkersOnEvalSet -v ./internal/knowledge/` | nothing (runs in `make test`) |

`scripts/smoke_knowledge.py` starts its own compose project
(`veda-knowledge-smoke`) with secrets in a temporary directory and its own
Neo4j volumes, reuses the developer's model cache volume offline, builds
VEDA, seeds synthetic ru/en articles in a public, a private and a
GitHub-connected source (the connection is written into the stopped store
with a publication copy that differs from the current revision and is
never due for sync, so GitHub is not contacted), waits for indexing and
checks: quotes against code-point offsets, public versus owner answers,
the publication copy, same paths in two sources, private evidence absent
from every public answer (also with the owner key on the public listener),
MCP answers equal to REST, reader-socket refusals, then restarts the
extractor and proves it is ready with no DNS or egress and re-extracts
after a forced reindex. It deletes its containers, volumes and temporary
files and prints only counts, booleans and timings.

### Results (2026-11-04)

Conditions: Apple M5 Max, 64 GB, macOS 27.0.1, Docker Desktop 29.9.2 (VM:
18 CPUs, 31.5 GiB), Go 1.26.3; about 37 unrelated containers running, load
average 2–6 during the runs. CPU extractor in Docker unless noted; one
VEDA worker.

End-to-end smoke (all checks passed):

| | 31 articles / 32 snapshots | 207 articles / 208 snapshots |
| --- | --- | --- |
| Time from VEDA start to all snapshots indexed | 23.6 s | 139.9 s (≈0.67 s per snapshot) |
| Evidence objects checked against offsets (all exact, all matching) | 598 | 23 904 |
| Owner REST p50 / p95, 50 sequential calls: entities | 3.8 / 4.6 ms | 7.4 / 8.5 ms |
| article | 7.5 / 8.7 ms | 15.3 / 19.7 ms |
| graph, depth 2 | 7.7 / 8.7 ms | 14.4 / 17.9 ms |
| context, depth 2 | 8.7 / 10.2 ms | 20.5 / 27.3 ms |
| Public graph, depth 2 (public corpus unchanged) | 4.9 / 7.8 ms | 4.4 / 5.9 ms |
| Extractor container memory (`docker stats`) | 1.86 GiB | 1.79 GiB |
| Neo4j container memory (heap 512 MiB–1 GiB, page cache 512 MiB) | 987 MiB | 1.02 GiB |
| VEDA RSS | 28 MB | 30 MB |
| Extractor ready after an offline restart | 6.2 s | 7.0 s |

The long article (one 5 179-code-point Cyrillic paragraph with emoji)
produced 68 evidence objects including the tail sentence. Extractor
`dropped` counters stayed 0.

Native extractor, one block per request, 3 runs (same machine and load):

| | CPU | MPS |
| --- | --- | --- |
| Load (`/ready` `load_seconds`) | not recorded (8.2–18.8 s in the extractor task) | 9.0 s |
| 65-code-point sentence | 0.09–0.11 s | 0.33 s (0.88 s first) |
| 5 157-code-point paragraph, 8 chunks | 2.40–2.47 s | 11.7–12.2 s |
| 30 evaluation sentences, median per request | 0.099 s | 0.413 s |
| Process RSS | 2.09 GB | 0.33 GB (weights in GPU memory, not in RSS) |

Earlier runs in the extractor task measured a dense 6 623-character block
at 8.7–9.1 s on an idle CPU and 29.7 s on MPS. CPU is the recommended
device.

### Evaluation

`services/knowledge-extractor/eval/sentences.jsonl` holds 30 hand-annotated
synthetic sentences (15 en, 13 ru, 2 mixed): negations, plans, examples,
past states, same-named entities of different types ("Apollo service" /
"Apollo project", "Mercury" the planet), mixed scripts and emoji. The same
author wrote the sentences and the marker lists, so marker results are
optimistic. Run on the real model (CPU; MPS gave identical results):

| | Precision | Recall | F1 |
| --- | --- | --- | --- |
| Entities, strict (exact surface) | 0.76 | 0.77 | 0.77 |
| Entities, lenient (containment) | 0.97 | 0.99 | 0.98 |
| Relations, strict | 0.46 | 0.56 | 0.51 |
| Relations, lenient | 0.65 | 0.79 | 0.72 |

Relations by type (lenient): `runs_on` P 1.00 R 0.81; `uses` 0.62 / 1.00;
`depends_on` 0.40 / 0.75; `part_of` 0.50 / 1.00; `maintained_by` 1.00 / 0.67;
`documents` 0 of 3 found.

Notable failure modes:

- In English the span often includes the type noun ("billing service",
  "host db-01", "payments project"). Since `normalize-v3` a leading type
  word is dropped from technical keys ("host db-01" → `db-01`); a trailing
  one is not (`billing service` keeps the key `billingservice` and does not
  merge with `billing`). The evaluation numbers above were measured with
  `normalize-v2`; entity scoring there compares spans, not keys.
- `uses` and `depends_on` are emitted together for the same pair (9 extra
  `depends_on`, 6 extra `uses`); treat them as one weak dependency signal.
- `documents` was never extracted; a runbook name was typed `document` once
  as "runbook" instead of its title.
- Plural objects lose the second relation ("depends on X and on Y").
- Ambiguous names: "Mercury" (a planet) became a `project`.
- Negation, plan, example and past are not seen by the model; VEDA's
  markers flagged all 13 such relations here, with the right category, but
  also 3 current relations that share a sentence with a marker
  (`go test -run TestMarkersOnEvalSet -v ./internal/knowledge/` prints them).
- Schema-valid output is not correct output: every response was valid
  (no dropped spans, all chunks `ok`) while about a third of the relations
  were wrong.
