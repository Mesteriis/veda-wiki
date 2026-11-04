# VEDA knowledge extractor

A small loopback HTTP service that runs the local GLiNER2.5 model
`fastino/gliner2.5-multi-v1` (revision `cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3`,
`gliner2==2.1.0`) over text blocks that VEDA (Go) sends. It only extracts: it never
reads files, paths, URLs or Git, never downloads, and keeps no state between requests.

## Run

```sh
# once, with network: put the pinned revision into the Hugging Face cache
uv run --project services/knowledge-extractor python scripts/fetch_knowledge_model.py
# key file shared with VEDA (0600, at least 16 bytes); never commit it
umask 077 && openssl rand -hex 32 > ~/.config/veda/extractor.key
# offline start
cd services/knowledge-extractor
HF_HUB_OFFLINE=1 uv run knowledge-extractor --key-file ~/.config/veda/extractor.key
```

Flags: `--host` (default `127.0.0.1`; anything else needs `--allow-non-loopback` and
at least one `--allowed-host` for the Host check), `--port` (8790), `--device cpu|mps|cuda`
(default `cpu`), `--cache-dir` (default `HF_HUB_CACHE`/`HF_HOME`), `--log-level`, and the
limits below. One uvicorn worker; the model is loaded once in a background thread.

`GET /health` answers 200 while the process lives. `GET /ready` answers 200 with
`{"status":"ready","model":{...},"load_seconds":...}` only after the model loaded *and*
a short self-test inference produced a valid span; otherwise 503 with
`{"status":"loading"|"error","reason":"..."}`. Reasons are operator-readable and contain
no paths or secrets, for example:

- `device mps unavailable: torch.backends.mps.is_available() is False` — no fallback to CPU;
- `model fastino/gliner2.5-multi-v1@cf5593a5… is not in the local Hugging Face cache; fetch it with: uv run --project services/knowledge-extractor python scripts/fetch_knowledge_model.py`.

Before gliner2, transformers or huggingface_hub are imported, the backend sets
`HF_HUB_OFFLINE=1` and `TRANSFORMERS_OFFLINE=1` with `os.environ.setdefault` (an explicit
operator value is kept). The checkpoint is always resolved with `local_files_only=True` and loaded from the
snapshot directory of the pinned revision (`AutoExtractor.from_pretrained(<snapshot>,
map_location=<device>)`). Loading from the directory matters: GLiNER2 loads the tokenizer
with `AutoTokenizer.from_pretrained(repo_or_dir)` without a revision, so a repo id would
pair pinned weights with the tokenizer of the `main` ref.

## Wire contract v1

`POST /v1/extract` with header `X-Veda-Extractor-Key` (compared in constant time) and
`Content-Type: application/json`. Offsets everywhere are half-open ranges of **Unicode code
points** within the block text; Python `str` indices are code points, so
`text[start:end]` is the quote (not UTF-8 bytes, not UTF-16 units).

Request (unknown fields are rejected):

```json
{"schema_version": 1, "request_id": "job-42",
 "schema": {"entities": {"service": "a running software service", "host": "a server or machine",
                         "technology": "a language, database or framework"},
            "relations": {"runs_on": {"head": ["service"], "tail": ["host"], "desc": "service is deployed on host"},
                          "depends_on": {"head": ["service"], "tail": ["service", "technology"], "desc": ""}}},
 "blocks": [{"id": "p1", "text": "Сервис billing 🚀 работает на хосте db-01 и зависит от PostgreSQL."}],
 "limits": {"max_entities_per_block": 200, "max_relations_per_block": 400}}
```

`limits` is optional and can only tighten the service caps. Entity/relation names match
`^[a-z][a-z0-9_]{0,63}$`; ids match `^[A-Za-z0-9._:/#-]{1,128}$`; block ids are unique;
block text must be valid Unicode (no lone surrogates).

Response (shape as returned; scores rounded, only part of the entities shown):

```json
{"schema_version": 1, "request_id": "job-42",
 "model": {"id": "fastino/gliner2.5-multi-v1", "revision": "cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3", "device": "cpu",
           "pipeline": {"model_id": "fastino/gliner2.5-multi-v1", "model_revision": "cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3",
                        "chunker_version": 1, "window_tokens": 512, "window_margin": 16, "overlap_tokens": 64,
                        "max_entities_per_block": 512, "max_relations_per_block": 1024,
                        "decoder": {"optimizer": "beam", "beam_size": 32, "top_k_entities": 32, "max_edges_per_type": 256,
                                    "candidate_threshold": 0.05, "relation_role_threshold": 0.05, "top_k_roles": 12,
                                    "count_top_k": 2, "relation_pair_cap": 128, "entity_threshold": null,
                                    "library": "gliner2==2.1.0"}}},
 "blocks": [{"id": "p1",
   "chunks": [{"start": 0, "end": 65, "feasible": true, "status": "ok"}],
   "entities": [{"local_id": "e1", "type": "service", "start": 7, "end": 14, "text": "billing", "confidence": 0.97},
                {"local_id": "e2", "type": "host", "start": 35, "end": 40, "text": "db-01", "confidence": 0.99}],
   "relations": [{"type": "runs_on", "head": "e1", "tail": "e2", "confidence": 0.90,
                  "head_confidence": 0.97, "tail_confidence": 0.99}],
   "dropped": {"invalid_spans": 0, "partial_spans": 0, "invalid_relations": 0, "over_limit": 0}}]}
```

- `model.pipeline` (also in `/ready`) lists every extractor setting that changes output:
  model id/revision, chunker version, window/margin/overlap, per-block caps and the JointIE
  decoder controls (pinned to the gliner2 2.1.0 defaults). VEDA folds it into the pipeline
  fingerprint; `chunker_version` is bumped whenever chunk plans can change.
- `local_id` is valid only inside its block result; relations reference these ids.
- `confidence` values are raw model scores in [0, 1], not probabilities of truth.
  `head_confidence`/`tail_confidence` are the endpoint scores from the chunk that
  produced the relation; relation `confidence` may be `null`.
- `chunks[].status`: `ok`; `truncated_prevented` (a sentence or word did not fit the
  window, so the chunk was cut at a word, or inside an over-long word — nothing was
  dropped); `infeasible` (JointIE returned `feasible=False`: the decoder could not satisfy
  the schema's hard constraints for that chunk — this is *not* "nothing found", and the
  chunk contributes no output).
- `dropped` counts model output removed by validation: spans out of bounds, with a quote
  that differs from `block[start:end]`, unknown types or invalid scores; relations with
  unknown/invalid endpoints or endpoint types outside the schema (including relations whose
  endpoint was a dropped chunk-edge partial); `partial_spans` are chunk-edge fragments (see
  Chunking); and items over the caps.

Errors are `{"error": {"code", "message"}}`: 401 `unauthorized`, 413 `body_too_large` /
`too_many_blocks` / `block_too_long`, 415, 422 `invalid_request` (field locations and error
kinds only, never input text) / `schema_too_large`, 503 `not_ready` / `busy`, 504 `timeout`.

## Limits

| flag | default | meaning |
| --- | --- | --- |
| `--max-body-bytes` | 2 MiB | request body (Content-Length and streamed size) |
| `--max-blocks` | 256 | blocks per request |
| `--max-block-chars` | 20000 | code points per block |
| `--request-timeout` | 120 s | checked between chunks; the slot is freed by the worker |
| `--inference-concurrency` | 1 | semaphore around inference; values other than 1 are refused |
| `--window-tokens` / `--window-margin` | 512 / 16 | encoder input budget = 496 tokens, schema prompt included |
| `--overlap-tokens` | 64 | overlap between consecutive chunks |

Per-block caps are 512 entities and 1024 relations (highest scores kept).

Inference is serialized: GLiNER2's processor mutates shared per-call state (inference mode
flag, token caches, collators) and concurrent JointIE calls on one model have not been
shown to be thread-safe, so `--inference-concurrency` must stay 1 and uvicorn runs one worker.

Compiled schemas live in the service's own LRU (16 entries, keyed by the SHA-256 of the
canonical schema JSON). Schemas are compiled with `gliner2.joint_ie.compile_schema` and
passed to `JointIE.extract` already compiled, so the engine's own unbounded
`_compiled_schemas` dict is never filled; evicted entries are released.

## Chunking

The encoder input for a chunk is the schema prompt, a separator and the text words
(`SchemaTransformer.transform_record`, which also appends a final `.` when the text has
no sentence punctuation). The budget is counted with the model's own tokenizer and word
splitter; every chunk is verified with the exact `len(transform_record(chunk).input_ids)`.
GLiNER2 itself does not truncate at inference (`max_len=None`), so the window is enforced
here, below the encoder's 512 absolute positions.

1. A block whose whole input fits is one chunk `[0, len(text))`.
2. Otherwise words are glued into whitespace-delimited runs (so boundaries never split
   punctuation, combining marks or ZWJ emoji); a run or word that alone exceeds the
   room is split, the latter into the longest character pieces that fit.
3. A chunk takes as many units as fit and ends at the last sentence end inside it
   (`.!?…` or a line break) that extends past the previous chunk; otherwise at a
   word boundary (`truncated_prevented`).
4. The next chunk starts up to `--overlap-tokens` back, at the earliest sentence start
   in that overlap, always moving forward. The last chunk ends at the last non-space
   code point: the tail is never dropped.
5. Chunk offsets are shifted to block offsets; each span is re-checked against the block,
   duplicates from overlaps are merged by `(type, start, end)` keeping the highest score,
   relations are merged by `(type, head span, tail span)`.

Chunk-edge partials: a span that, in every chunk producing it, touches an interior chunk
edge (start at the chunk start or end at the chunk end; block edges excluded) is dropped
when another chunk produced a same-type span containing it that continues past that edge —
e.g. `Иван` at the end of one chunk versus `Иван Петров` in the overlapping next one.

Relations are only taken from one chunk, so both endpoints come from the same context;
cross-chunk and cross-document links are built by Go from confirmed mentions.
The GLiNER2 long-text helpers (`extract_long`) are not used.

## Extraction path: JointIE

`gliner2.joint_ie.JointIE` over the `AutoExtractor`-loaded checkpoint (boundary
architecture) with a `JointSchema` of entity types (with descriptions) and relations with
allowed head/tail types; `JointIEConfig(include_confidence=True, include_spans=True)`,
default beam optimizer. Evidence from the real checkpoint (see the task report):

- JointIE returns `feasible`, entities `{id,type,text,start,end,confidence}` and relations
  `{type,head,tail,confidence}` with local ids — exactly the fields needed; typed endpoints
  are enforced by the compiled constraints (`TypedEndpoints`, `NoSelfLoops`, unique pairs).
- `model.extract` with entities+relations returned relation endpoints that disagree with the
  extracted entities (e.g. relation head `Сервис billing` while the entity was `billing`)
  and does not check endpoint types; it was therefore not chosen.
- Relation descriptions are not part of the JointIE model prompt in gliner2 2.1.0 (only
  entity descriptions are); `desc` is accepted for the contract and versioning.
- The beam decoder dominates latency on dense text (about 1–3.5 s per 500-token chunk on
  an M5 Max CPU, encoder ~0.15 s). The greedy optimizer was 8× faster on the probe set but
  added `part_of service → organization` relations that beam did not, so beam stays.

## Devices

CPU is the baseline. `--device mps` was verified natively on macOS (Apple M5 Max): it loads
and passes the real-model tests but was slower than CPU for this workload. `--device cuda`
is accepted, but on Linux the lock installs the CPU-only torch build, so `/ready` reports
`device cuda unavailable` unless the torch source is changed — there is no silent fallback.

## Tests

`make test` does not run these tests: `uv sync` needs network access, while `make test`
must work offline. CI runs `make extractor-test` as its own step after `make test`.

```sh
make extractor-test   # unit tests, fake model, HF_HUB_OFFLINE=1, no network
make extractor-real   # the pinned checkpoint from the local cache (EXTRACTOR_TEST_DEVICE=mps to try MPS)
```

Logs carry request ids, block counts, sizes, statuses and timings — never text or keys.
