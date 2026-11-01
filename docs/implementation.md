# Implementation ledger

The complete intended product includes every requirement below. Completion of
an individual slice is not completion of the whole application.

| Requirement | Current evidence / remaining work |
| --- | --- |
| Standalone OSS React + Go, no private corpus/config | Published source-only repository; empty-store browser check |
| Reading-only frontend; authors use REST/MCP/repositories | No article editor or creation controls; shared REST/MCP write service |
| Local durable articles, revisions and independent source identity | Store/restart/race/concurrency tests; bounded JSON engine currently |
| Merge multiple sources for reading while keeping them distinct | Source-keyed index/search; remote synchronization implemented; real multi-repository acceptance pending |
| Wiki metadata in veda.yaml and hierarchical Markdown | Strict schema, checkout import and nested frontend folder navigation |
| Program docs/ only, no docs means skip | Originals and version-bound Russian translations stored separately; optional loopback Ollama worker requires live-model and translation-quality acceptance |
| GitHub App/account or explicit server token, repo-derived visibility | GitHub App/PAT and repository visibility implemented; live authenticated acceptance pending |
| Background import/push, local availability during outages | Atomic import/conflicts and scheduled durable outbox implemented; live write acceptance pending |
| Transfers between repositories with provenance and publication control | Revision-bound moves and lost-response recovery tested with simulated repositories; a live synthetic move passed across two public repositories through a `gh` proxy; translations transfer as private drafts between program sources; real VEDA credential acceptance and linked dependencies remain pending |
| Private users/teams/agents and public browsing | Separate owner/read-only keys, local read-only socket, isolated public listener, reader sessions and limited GitHub administrator sessions; per-user/team permissions pending |
| Agent context and the four client integrations | Four client configurations use the VEDA read-only socket; individual scoped keys and separate operating-system identities are not rolled out |
| Safe production migration, backup/rollback and restore acceptance | Standalone instance deployed alongside the existing wiki; backup and isolated restored-state checks passed; full guest restore and production v4 rollback remain unverified |

Additional engineering work: translation provenance and stale-state handling,
historical image bindings, blob/history retention, scale-appropriate database
migration, packaging and whole-product acceptance. Future verification must
include actual multi-repository synchronization, failure recovery and all
access paths, not just the existing unit tests.
