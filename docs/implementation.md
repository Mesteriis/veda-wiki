# Implementation ledger

The complete intended product includes every requirement below. Completion of
an individual slice is not completion of the whole application.

| Requirement | Current evidence / remaining work |
| --- | --- |
| Standalone OSS React + Go, no private corpus/config | Published source-only repository; empty-store browser check |
| Reading-only frontend; authors use REST/MCP/repositories | No article editor or creation controls; shared REST/MCP write service |
| Local durable articles, revisions and independent source identity | Store/restart/race/concurrency tests; bounded JSON engine currently |
| Merge multiple sources for reading while keeping them distinct | Source-keyed index/search and synchronization; synthetic live transfer across two public repositories passed via a `gh` proxy; acceptance with VEDA-stored credentials remains pending |
| Wiki metadata in veda.yaml and hierarchical Markdown | Strict schema, checkout import and nested frontend folder navigation |
| Program docs/ only, no docs means skip | Originals and version-bound Russian translations stored separately; optional loopback Ollama worker requires live-model and translation-quality acceptance |
| GitHub App/account or explicit server token, repo-derived visibility | GitHub App/PAT and repository visibility implemented; live authenticated acceptance pending |
| Background import/push, local availability during outages | Atomic import/conflicts and scheduled durable outbox implemented; live write acceptance pending |
| Transfers between repositories with provenance and publication control | Revision-bound linked-article and image bundles, publication review and lost-response recovery tested with simulated repositories and a synthetic live transfer through `gh`; translations remain private drafts; real VEDA credential acceptance and production v10 restore remain pending |
| Private users/teams/agents and public browsing | Separate owner/read-only keys, local read-only socket, isolated public listener, reader sessions and limited GitHub administrator sessions; per-user/team permissions pending |
| Agent context and the four client integrations | Four client configurations use the VEDA read-only socket; MCP can prepare a non-persistent, revision-bound proposal; individual scoped keys and separate operating-system identities are not rolled out |
| Safe production migration, backup/rollback and restore acceptance | Standalone instance deployed alongside the existing wiki; isolated restore and queue-resume rehearsals passed through synthetic v10 and an earlier production v1 snapshot; restoring actual production data written in newer formats and rollback without data loss remain unverified |

Additional engineering work: translation provenance and stale-state handling,
historical image bindings, blob/history retention, scale-appropriate database
migration, packaging and whole-product acceptance. Future verification must
include actual multi-repository synchronization, failure recovery and all
access paths, not just the existing unit tests.
