# Russian translations of program documentation

Program sources import only `docs/`. The original Markdown and its revisions
remain authoritative; a Russian translation is stored separately on the VEDA
server and is not pushed into the source repository. The reader displays the
translation only when it matches the exact original SHA; when the original
changes it displays the original until a fresh translation is submitted.
The article view labels the translation and shows both hashes. When no usable translation is visible, program documentation is labeled as the
original. An authenticated reader sees when an existing translation is stale;
the public view reports it as unavailable without disclosing unpublished drafts.
REST/MCP article
reads return the original, allowing an agent to translate from the actual source.
`veda_read_display` and `GET /api/v1/articles/display` return the Markdown shown
to the caller, including a currently visible Russian translation when available,
its state and hashes. They do not return an editable revision; use `veda_read`
and the original revision for update proposals. Unapproved translations remain
private, and stale translations are never returned as the displayed text.
For large documents, `veda_read_display_excerpt` reads up to 16,000 Unicode
code points at a time. Continue with both the displayed and original hashes
from the first response; a change to either produces a conflict.
New drafts produced by the local worker retain `origin: local_model` across
restart, copying, moving, and unchanged approval. The reader labels those
translations as automatic. An edited submission has unknown origin; older
translations and API submissions are not assumed to be human-authored.
Recording this provenance upgrades the store to version 11, which older
servers reject; back up and verify a compatible restore before upgrading.

An owner or authenticated reader can list candidates with `GET /api/v1/articles/translation-needed`
(`source_id`, `limit` 1–100 and `offset`) or `veda_translation_needed`. The
optional source filter applies before pagination; an inaccessible or unknown
source has an empty result, without confirming whether it exists. Scoped readers see
only their authorized sources; anonymous callers cannot list the queue. The
queue reports `needed`, `publication_review`, or `blocked_conflict`; the last
state requires resolving an import conflict before translation or publication.
The automatic worker skips blocked candidates. The
owner can read each original and submit via `POST /api/v1/articles/translation` or `veda_translate_article`
with `source_id`, `path`, `expected_revision`, `original_sha256`, `text` and
optional `confirm_publication`. The original revision and SHA must still match.
to protect concurrent translation edits, pass `expected_translation_fingerprint`
from a recent validation or MCP proposal. An empty string means no translation
existed at preparation; a nonempty fingerprint identifies the entire stored
translation including approval and provenance. A changed translation returns 409
even if the original revision is unchanged. This precondition is optional for
compatibility, so older submissions that omit it can still overwrite one another.
An unresolved import conflict also blocks validation and saving, even if the
local revision and hash remain unchanged. Resolve the conflict before saving.
Code blocks, inline code, link and image
destinations and raw HTML fragments must be retained in the same order. HTML
blocks are preserved in full, including any prose inside them; prose inside an
HTML block cannot currently be translated through this submission path. This
structural check cannot
prove translation accuracy, preserve every kind of Markdown construct, or
replace human review.

Read-only MCP clients can use `veda_prepare_translation` to return an unsaved
proposal bound to the current original revision, SHA and translation
fingerprint. It accepts program
`docs/` articles visible to that client and rejects a stale revision. The
server returns structural, Cyrillic-prose, unchanged-text and credential-pattern
checks through `POST /api/v1/articles/translation-validate`. The validation
response and MCP proposal include source status from the same snapshot as the
revision check; review coverage and sync warnings before acting. The updated
MCP adapter requires a server that returns this status. This POST is a
non-mutating exception for authenticated readers and the local reader socket;
browser-origin and public-listener requests are refused. The checks cannot prove
translation quality or completeness, and a successful result does not save a
draft or approve publication. Source changes, conflicts, move state, access or
publication requirements can still prevent later submission. An authorized
client must review and submit through the existing translation API. The tool
does not translate text by itself.

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
`--translation-protocol mlx`. For a local service supporting OpenAI-compatible
chat completions, use `--translation-protocol openai`; this sends no MLX-specific
request options. The default protocol is `ollama`. Both URL and
model flags are required; the worker is disabled by
default. Its URL must use a numeric loopback address, HTTP, and an explicit port;
HTTP proxies and redirects are disabled. The operator must verify that the
local service and model do not forward documents elsewhere. If the local service
requires a bearer, `--translation-token-file /absolute/private/path` reads it
from an owner-only regular file owned by the VEDA process user; the file must
contain only the token, without a newline. The final path cannot be a symlink.
The file is read again on each request to allow rotation. An inaccessible or
invalid file stops that translation attempt without sending the document or
the token; credentials are not placed in command arguments or logs. Supplying
this option requires the translation URL and model. The CI smoke test runs the
actual VEDA binary with synthetic Ollama and OpenAI-compatible loopback services,
checks bearer rotation and reopens saved translations after restart; it does not
exercise a real model.
Configure `--translation-interval` between 5 seconds and 1 hour (default one minute).

The worker processes one candidate per interval and retries failures after 15
minutes. Documents over 32,000 bytes are split at top-level paragraph or
heading boundaries, then validated and saved as one result.
Between requests the worker rechecks the original revision, conflicts, deletion,
move state and whether an owner already supplied a translation; when a change
is observed it stops sending later chunks. A change can race with a request
already in flight, so the final save also rechecks state under the write lock.
The result is discarded if it is no longer current. A split is rejected
if it changes how protected Markdown is parsed, including references whose
definitions are in another chunk. Large blocks without a safe split require manual handling and are skipped for
24 hours. Large documents containing HTML can be split only when all HTML is
in the final chunk; otherwise they also require manual handling. This prevents
a chunk boundary from crossing an HTML element that remains open after a blank
line. HTML blocks themselves are preserved verbatim. It does not detect the original language or verify translation
quality. It rejects unchanged output, truncated generation, stale revisions and
changes to protected Markdown, including heading levels and list nesting, type, starting number and item
count. This does not prove that translated prose preserves meaning.
Table rows, column structure and alignment, and task-list checkbox states must
also remain unchanged. As with other structural checks, this cannot detect a
translation that changes the meaning or order of ordinary prose within cells.
The stricter check applies to new validation and submissions; previously saved
translations are not automatically revalidated or hidden.
The worker also requires at least three Cyrillic
letters and at least 40% Cyrillic among Latin and Cyrillic letters in parsed
prose; code blocks, inline code, autolinks and raw HTML do not count. This is
a conservative script check, not language identification or quality review:
code-only or identifier-heavy documents and valid mixed-language translations
may be rejected and require manual handling. A public article's machine translation remains
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
