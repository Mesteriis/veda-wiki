# Content model

VEDA owns a local versioned article store. Connected repositories synchronize in the
background without becoming the only durable copy. Multiple sources stay
separate and are combined for reading/search; source ID plus path identifies
an article. Matching paths do not cause automatic cross-source replacement.

## Repository types

- Wiki: root `veda.yaml` with versioned metadata (title, description, optional
  image/link), plus a Markdown hierarchy and scoped `AGENTS.md` instructions.
- Program: import only `docs/`. No `docs/` means no documentation import. Keep
  code blocks, original provenance and links while translating English docs
  to Russian. Keep translations distinct from their upstream originals.

Public/private access inherits repository visibility once a repository is
connected; folder names and YAML cannot override it. Unknown remote visibility
fails closed. Local sources in the initial release have owner-selected visibility
and are not connected to any repository.

GitHub connections use an explicitly selected GitHub App/account. An optional
server-held token can support installations without an App. Never guess an
account by trying unrelated tokens. Import permissions and outbound write
permissions are separate. Do not send private source text to a translation
provider without owner authorization.

## Synchronization and moves

Connected sources have a durable outbox and per-source status. Conflicts preserve
both versions and outages retain accepted local writes. See [GitHub synchronization](github.md).

A move across repositories must preserve provenance, wait for destination
durability before removing the source, and report independent remote sync
failures. Private-to-public transfer is an explicit publication operation with
permission/content checks. Private revision history must not become public as a
side effect. Hiding a previously public article cannot erase external Git history
or previously downloaded copies.

The local importer implements metadata ingestion and program docs/ selection;
see [repository import](import.md). Revision-bound moves are available with
the restrictions documented in [the API](api.md). Per-user/team access,
acceptance with VEDA-stored GitHub credentials and translation model
qualification require further work.
