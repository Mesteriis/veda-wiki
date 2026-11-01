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
| Program docs/ only, no docs means skip | Local checkout tests; English→Russian translation still pending |
| GitHub App/account or explicit server token, repo-derived visibility | GitHub App/PAT and repository visibility implemented; live authenticated acceptance pending |
| Background import/push, local availability during outages | Atomic import/conflicts and scheduled durable outbox implemented; live write acceptance pending |
| Transfers between repositories with provenance and publication control | Not implemented |
| Private users/teams/agents and public browsing | Owner token and public listener isolated; scoped credentials, browser sessions and admin Connect UI pending |
| Agent context and the four client integrations | Context tools implemented; production clients not yet migrated to the standalone server |
| Safe production migration, backup/rollback and restore acceptance | Current instance deliberately retained; standalone deployment/migration pending |

Additional engineering work: translation provenance and stale-state handling,
historical image bindings, blob/history retention, scale-appropriate database
migration, packaging and whole-product acceptance. Future verification must
include actual multi-repository synchronization, failure recovery and all
access paths, not just the existing unit tests.
