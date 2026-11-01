# Russian translations of program documentation

Program sources import only `docs/`. The original Markdown and its revisions
remain authoritative; a Russian translation is stored separately on the VEDA
server and is not pushed into the source repository. The reader displays the
translation only when it matches the exact original SHA; when the original
changes it displays the original until a fresh translation is submitted.
The article view labels the translation and shows both hashes. REST/MCP article
reads return the original, allowing an agent to translate from the actual source.

The owner can list candidates with `GET /api/v1/articles/translation-needed`
(`limit` 1–100 and `offset`) or `veda_translation_needed`, read each original,
and submit via `POST /api/v1/articles/translation` or `veda_translate_article`
with `source_id`, `path`, `expected_revision`, `original_sha256`, `text` and
optional `confirm_publication`. The original revision and SHA must still match;
unresolved conflicts block submission. Code blocks, inline code, link and image
destinations must be retained in the same order. This structural check cannot
prove translation accuracy, preserve every kind of Markdown construct, or
replace human review.

A translation visible to the public requires explicit publication approval;
credential-like text is refused. A translation saved privately cannot become
public automatically if visibility changes: it appears as `publication_review`
in the candidate list and must be reviewed and resubmitted with approval.
An optional background worker can use a separately installed Ollama service on
the same host, for example `--translation-local-url http://127.0.0.1:11434
--translation-model MODEL`. Both flags are required; the worker is disabled by
default. Its URL must use a numeric loopback address, HTTP, and an explicit port;
HTTP proxies and redirects are disabled. The operator must verify that the
local service and model do not forward documents elsewhere. Configure
`--translation-interval` between 5 seconds and 1 hour (default one minute).

The worker processes one candidate per interval, retries failures after 15
minutes, and skips documents above 32,000 bytes for 24 hours; those require
manual handling. It does not detect the original language or verify translation
quality. It rejects unchanged output, truncated generation, stale revisions and
changes to protected Markdown. A public article's machine translation remains
a private draft with `publication_review` until the owner approves it through
the existing REST/MCP submission. A concurrent owner translation is never
overwritten by the worker. Preserve and review translations before publication;
structural checks do not detect every possible semantic or formatting error.
