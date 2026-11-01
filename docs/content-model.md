# Content model

VEDA owns a local versioned article store. Repositories will synchronize in the
background without becoming the only durable copy. Multiple sources stay
separate and are combined for reading/search; source ID plus path identifies
an article. Matching paths do not cause automatic cross-source replacement.

## Planned repository types

- Wiki: root `veda.yaml` with versioned metadata (title, description, optional
  image/link), plus a Markdown hierarchy and scoped `AGENTS.md` instructions.
- Program: import only `docs/`. No `docs/` means no documentation import. Keep
  code blocks, original provenance and links while translating English docs
  to Russian. Keep translations distinct from their upstream originals.

Public/private access inherits repository visibility once a repository is
connected; folder names and YAML cannot override it. Unknown remote visibility
fails closed. Local sources in the initial release have owner-selected visibility
and are not connected to any repository.

GitHub Connect will use an explicitly selected GitHub App/account. An optional
server-held token can support installations without an App. Never guess an
account by trying unrelated tokens. Import permissions and outbound write
permissions are separate. Do not send private source text to a translation
provider without owner authorization.

## Synchronization and moves (not implemented)

Local changes need a durable outbox, tracked base commit/blob and per-source
status. Import must not overwrite a concurrently modified local revision.
Conflicts preserve both versions. An outage retains the local accepted write.
Do not treat the current local transaction as an already implemented outbox.

A move across repositories must preserve provenance, wait for destination
durability before removing the source, and report independent remote sync
failures. Private-to-public transfer is an explicit publication operation with
permission/content checks. Private revision history must not become public as a
side effect. Hiding a previously public article cannot erase external Git history
or previously downloaded copies.

The initial implementation intentionally has no source visibility update or move
operation. Per-user/team access, repository connectors, metadata ingestion and
translation workers require further implementation and tests.
