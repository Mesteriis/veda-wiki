# Implementation ledger

The complete intended product includes every requirement below. Completion of
an individual slice is not completion of the whole application.

| Requirement | Current evidence / remaining work |
| --- | --- |
| Standalone OSS React + Go, no private corpus/config | Published source-only repository; empty-store browser check |
| Reading-only frontend; authors use REST/MCP/repositories | No article editor or creation controls; shared REST/MCP write service |
| Local durable articles, revisions and independent source identity | Store/restart/race/concurrency tests; optional SQLite migration and >64 MiB synthetic test added, production migration and restore acceptance pending |
| Merge multiple sources for reading while keeping them distinct | Source-keyed index/search and synchronization; synthetic live transfer across two public repositories passed via a `gh` proxy; acceptance with VEDA-stored credentials remains pending |
| Wiki metadata in veda.yaml and hierarchical Markdown | Strict schema, checkout import and nested frontend folder navigation |
| Program docs/ only, no docs means skip | Originals and version-bound Russian translations stored separately; optional loopback Ollama worker requires live-model and translation-quality acceptance |
| GitHub App/account or explicit server token, repo-derived visibility | GitHub App/PAT and repository visibility implemented; a simulated private GitHub App installation verifies JWT signatures, repository-scoped read/write permissions, stored credential reload, import and write-back. Anonymous live public-program import, one upstream content comparison and restart passed on a workstation; the deployed host was rate-limited. Live authenticated acceptance pending |
| Background import/push, local availability during outages | Atomic import/conflicts and scheduled durable outbox implemented; live write acceptance pending |
| Transfers between repositories with provenance and publication control | Revision-bound linked-article and image bundles, publication review and lost-response recovery tested with simulated repositories and a synthetic live transfer through `gh`; translations remain private drafts; real VEDA credential acceptance and production v10 restore remain pending |
| Private users/teams/agents and public browsing | Separate owner/read-only keys, source-scoped browser and API reader sessions, key-management and GitHub administrator sessions, local read-only socket and isolated public listener; user/team accounts pending |
| Agent context and the four client integrations | Four client configurations use the VEDA read-only socket; MCP can prepare a non-persistent, revision-bound proposal and read revision-bound excerpts. Claude Code, OpenCode 2.2.5 and the Hermes agent core each called source, read, excerpt and proposal tools against a synthetic store and local model stub; a scoped key refused access to a second private source. The Hermes core test did not exercise its gateway chat. These tests do not exercise model decisions or production identities. Individual Codex calls, Hermes gateway chat, scoped production keys and separate operating-system identities remain unverified |
| Safe production migration, backup/rollback and restore acceptance | Standalone instance deployed alongside the existing wiki; synthetic v13 backup/restore and queue-resume passed. An offline restore of production v13 matched the saved state and retained three large articles; booting the restored guest and rollback without data loss remain unverified |

Additional engineering work: richer translation provenance and stale-state handling
(new local-model drafts are labelled; older and externally submitted translations
retain unknown origin),
historical image bindings, blob/history retention, database indexing, retention and production migration, packaging and whole-product acceptance. Future verification must
include actual multi-repository synchronization, failure recovery and all
access paths, not just the existing unit tests.
