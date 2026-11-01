# Russian translations of program documentation

Program sources import only `docs/`. The original Markdown and its revisions
remain authoritative; a Russian translation is stored separately on the VEDA
server and is not pushed into the source repository. The reader displays the
translation only when it matches the exact original SHA; when the original
changes it displays the original until a fresh translation is submitted.
The article view labels the translation and shows both hashes. REST/MCP article
reads return the original, allowing an agent to translate from the actual source.

An owner or authenticated reader can list candidates with `GET /api/v1/articles/translation-needed`
(`limit` 1–100 and `offset`) or `veda_translation_needed`. Scoped readers see
only their authorized sources; anonymous callers cannot list the queue. The
owner can read each original and submit via `POST /api/v1/articles/translation` or `veda_translate_article`
with `source_id`, `path`, `expected_revision`, `original_sha256`, `text` and
optional `confirm_publication`. The original revision and SHA must still match;
unresolved conflicts block submission. Code blocks, inline code, link and image
destinations must be retained in the same order. This structural check cannot
prove translation accuracy, preserve every kind of Markdown construct, or
replace human review.

Read-only MCP clients can use `veda_prepare_translation` to return an unsaved
proposal bound to the current original revision and SHA. It accepts program
`docs/` articles visible to that client and rejects a stale revision. It does
not check translation quality or Markdown structure, save the draft, or grant
publication approval. An authorized client must submit and validate it through
the existing translation API. The tool does not translate text by itself.

A translation visible to the public requires explicit publication approval;
credential-like text is refused. A translation saved privately cannot become
public automatically if visibility changes: it appears as `publication_review`
in the candidate list and must be reviewed and resubmitted with approval.
Copying or moving an article with a translation to another program source keeps
the translation as a private draft even when it was previously approved. A
translation cannot be transferred to a wiki source. A move with translation
uses store format version 5; older binaries cannot open the upgraded store.
An optional background worker can use a separately installed Ollama service on
the same host, for example `--translation-local-url http://127.0.0.1:11434
--translation-model MODEL`. Alternatively, use a local MLX LM server and set
`--translation-protocol mlx`; the default protocol is `ollama`. Both URL and
model flags are required; the worker is disabled by
default. Its URL must use a numeric loopback address, HTTP, and an explicit port;
HTTP proxies and redirects are disabled. The operator must verify that the
local service and model do not forward documents elsewhere. Configure
`--translation-interval` between 5 seconds and 1 hour (default one minute).

The worker processes one candidate per interval and retries failures after 15
minutes. Documents over 32,000 bytes are split at top-level headings outside
code fences, then validated and saved as one result. Large sections without a
safe split and large documents containing HTML require manual handling and are
skipped for 24 hours. It does not detect the original language or verify translation
quality. It rejects unchanged output, truncated generation, stale revisions and
changes to protected Markdown. A public article's machine translation remains
a private draft with `publication_review` until the owner approves it through
the existing REST/MCP submission. A concurrent owner translation is never
overwritten by the worker. Preserve and review translations before publication;
structural checks do not detect every possible semantic or formatting error.
An opt-in synthetic MLX integration test can be run with `VEDA_TEST_MLX_URL`
and `VEDA_TEST_MLX_MODEL` pointing at an already running numeric-loopback server
and local model. One synthetic example passed on macOS with a local 1.7B model;
that same model failed the opt-in test on VEDA's public `docs/import.md`: it
changed protected Markdown elements, so the worker rejected the output. A
second experiment using shorter sections also failed structural validation.
Masking code with markers was also tried: the model changed or omitted the
markers on the same project document, so the experiment was not retained.
This does not establish model quality, production security or LXC availability.
