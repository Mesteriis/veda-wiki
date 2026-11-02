# Implementation ledger

The complete intended product includes every requirement below. Completion of
an individual slice is not completion of the whole application.

| Requirement | Current evidence / remaining work |
| --- | --- |
| Standalone OSS React + Go, no private corpus/config | Published source-only repository; empty-store browser check |
| Reading-only frontend; authors use REST/MCP/repositories | No article editor or creation controls; shared REST/MCP write service |
| Local durable articles, revisions and independent source identity | Store/restart/race/concurrency tests; SQLite migration, >64 MiB synthetic test and deployment migration passed; indexing and retention remain pending |
| Merge multiple sources for reading while keeping them distinct | Source-keyed index/search and synchronization; two simulated repositories with a shared path passed scoped MCP read/search/proposal checks after SQLite restart. Two anonymously cloned public program repositories passed the local-checkout import, SQLite restart and MCP merged search/read/proposal check, with one exact article comparison per source. Synthetic live transfer across two public repositories passed via a `gh` proxy; anonymous multi-source GitHub API import, SQLite restart and MCP search/read/proposals also passed on the deployment host; a two-source SQLite test also verifies independent privacy changes, outage and recovery; acceptance with VEDA-stored credentials remains pending |
| Wiki metadata in veda.yaml and hierarchical Markdown | Strict schema, checkout import and nested frontend folder navigation. A real public synthetic wiki fixture branch and a distinct public program checkout passed metadata/image, docs-only, SQLite restart and MCP checks. The same mixed source types also passed a read-only real GitHub API check through a credential-owning `gh` proxy, SQLite restart, a simulated single-source outage/recovery and scoped MCP search/read/proposals; an anonymous mixed wiki/program API import also passed on the deployment host with metadata, image, docs-only selection, SQLite restart and MCP reads/proposals. VEDA's saved credentials remain unverified |
| Relative links and images across sources | A synthetic private/public pair with colliding paths passed source-qualified link, image, scoped/public access, independent image update and SQLite restart checks. A mixed-type real GitHub API read through `gh` also verified the wiki image; authenticated private source coverage remains pending |
| Program docs/ only, no docs means skip | Originals and version-bound Russian translations stored separately; JSON/SQLite tests and MCP SDK with a local Git checkout verify that an unresolved import conflict blocks translation even when the local revision is unchanged. Optional loopback Ollama, MLX and OpenAI-compatible workers require live-model and translation-quality acceptance. The installed Linux binary passed a synthetic OpenAI-compatible service check with bearer rotation and process restart; no real model was exercised |
| GitHub App/account or explicit server token, repo-derived visibility | GitHub App/PAT and repository visibility implemented; a simulated private GitHub App installation verifies JWT signatures, repository-scoped read/write permissions, stored credential reload, import and write-back. Anonymous live public-program import passed on both a workstation and the deployment host; one upstream content comparison and restart passed on the workstation. A real anonymous browser Connect and docs-only import of a public program repository passed on a workstation; live authenticated acceptance remains pending |
| Background import/push, local availability during outages | Atomic import/conflicts and scheduled durable outbox implemented; simulated lost responses recover after SQLite reopen without a duplicate push, including when a second repository has the same article path. A fresh test subprocess also reopened SQLite and acknowledged an ambiguous push without a duplicate against a simulated GitHub service. A synthetic REST write, simulated outage, SQLite reopen and queued remote write were also verified against public GitHub through a `gh` proxy; this was not a process restart. Live VEDA-credential write acceptance remains pending |
| Transfers between repositories with provenance and publication control | Revision-bound linked-article and image bundles, publication review and lost-response recovery tested with simulated repositories and a synthetic live transfer through `gh`; an index-to-README directory move also passed on two public VEDA branches through that proxy. Translations remain private drafts; real VEDA credential acceptance and production v10/v17 restore remain pending |
| Private users/teams/agents and public browsing | Separate owner/read-only keys, source-scoped browser and API reader sessions, key-management and GitHub administrator sessions, local read-only socket and isolated public listener. Named read-only users, teams and password changes are deployed; no real users or teams have been created, so production account acceptance remains pending. MFA, SSO and process isolation are not implemented |
| Agent context and the four client integrations | Four client configurations use the VEDA read-only socket; MCP can prepare a non-persistent, revision-bound proposal and read revision-bound excerpts. Claude Code, OpenCode 2.2.5 and the Hermes agent core each called source, read, excerpt and proposal tools against a synthetic store and local model stub; a scoped key refused access to a second private source. The Hermes core test did not exercise its gateway chat. These tests do not exercise model decisions or production identities. Four independently keyed synthetic MCP sessions also passed the SSH route to the deployment host, including source denial, proposals and independent revocation; this did not run the four agent clients. Claude Code, OpenCode 2.2.5 and Hermes core also called sources, a bounded excerpt and an unsaved new-article proposal against the deployment instance over SSH with temporary profiles and local deterministic model stubs. These checks did not exercise a real model, Hermes gateway or separate agent identities. Individual Codex calls, Hermes gateway chat, scoped production keys and separate operating-system identities remain unverified |
| Safe production migration, backup/rollback and restore acceptance | Standalone instance deployed alongside the existing wiki; synthetic SQLite backup/restore, isolated guest boot and queue resume passed. A PBS snapshot during synthetic writes restored and started. A restored production SQLite snapshot matched the backup and passed integrity checking; its guest later booted without a network interface, VEDA became active, health returned HTTP 200 and the public article list was empty. One private article was read through the local read-only socket and its revision hash verified. Reading with production credentials and rollback without losing later changes remain unverified |
| Portable Linux service | Example systemd unit and installation instructions; syntax checked on the deployment host. An isolated Debian LXC first-start rehearsal with fixture-specific paths and ports created a dedicated user and private state/key files, and passed health after restart. An earlier rehearsal failed without a classified cause, then passed with a readiness wait. An explicit-allowlist Linux archive can be built locally; its amd64 binary passed a temporary LXC health, embedded UI and empty-store check. An ARM64 archive passed synthetic REST, public isolation, UI, SQLite restore and local translation protocol checks in a networkless Linux ARM64 container. Full installation from the instructions on a fresh host and published prebuilt releases remain unverified |

Additional engineering work: richer translation provenance and stale-state handling
(new local-model drafts are labelled; older and externally submitted translations
retain unknown origin),
historical image bindings, blob/history retention, database indexing, packaging and whole-product acceptance. Future verification must
include actual multi-repository synchronization, failure recovery and all
access paths, not just the existing unit tests.

Synthetic GitHub lost-response tests cover both JSON and SQLite backends when a
later edit follows an uncertain prepared write. They reopen the store, confirm
both revisions without a duplicate push, and preserve an unresolved conflict
until the prepared content is observed. The two-revision SQLite scenario also
resumes from a fresh test subprocess against a synthetic GitHub service. This
does not test stored real GitHub credentials or a process crash during an
in-flight request.

A two-repository synthetic GitHub test also covers an invalid `docs` entry in
one source after SQLite migration: its local article remains available, its
public snapshot closes, and the second source remains public and unchanged.
Both states survive reopening; a corrected first source can be synchronized
again. This does not establish behavior for VEDA-stored real credentials.

A separate two-source program-import test uses the same Markdown path and
original text in a public and a private source. After SQLite reopen, each source
retains its own Russian translation; importing a changed original into the
public source hides its stale translation while the other source is unaffected.
This checks versioning and visibility, not translation quality or a real model.

Historical image bindings need more than retaining blobs. Today an article
revision stores only Markdown and its text hash, while the source stores one
current path-to-blob map. An image can change without the Markdown hash changing;
the current map therefore cannot reconstruct earlier images. A future storage
format must record an image snapshot independently of the text hash, preserve
the distinction between an unknown legacy snapshot and an intentionally empty
one, and update it atomically with the applicable import or write. It must also
cover published snapshots, transfers, conflict resolution, translations and
restart/restore. Older revisions cannot safely be backfilled from the current
map as though it were historical evidence. A revision-specific media route must
check access to the source and revision before serving a blob, rather than
exposing arbitrary content by digest. Until those paths are implemented and
tested together, revision history is text history only.

When no move is pending, SQLite article creation and update persist the changed
article, outbox jobs, move states and metadata in one transaction. A pending
move or another mutation uses the full transaction because reconciliation can
change a second article. A synthetic benchmark with 200 approximately 126 KiB documents
measured approximately 42 ms and 31.7 MB allocated per single-article update
before the targeted write path, and approximately 2.3 ms and 0.9–1.2 MB afterwards
on an Apple M5 Max
(`cd server && go test -run '^$' -bench '^BenchmarkSQLiteSingleArticleUpdateLargeCorpus$' -benchmem -benchtime=3x`).
This is not a production workload or a storage limit. The specialized path is
only for `put`; any future change to the categories it mutates must update that
transaction and its recovery tests. Shallow snapshots alone are not sufficient
evidence that nested values cannot change in place.

For compact search, one request-local buffer is reused while ranking visible
revisions; Unicode text retains `strings.ToLower` semantics. No global cache or persisted index is introduced. On the synthetic
200-document benchmark `BenchmarkFindArticlesLargeDocumentsNoExactPhrase`,
three iterations on an Apple M5 Max measured about 41 ms and 185 KB allocated
per request versus an earlier two-iteration measurement of about 45 ms and
26.3 MB. A comparable 200-document Russian synthetic benchmark measured about 143 ms
and 216 KB after the change versus about 160 ms and 29.6 MB before it. These
are limited synthetic measurements, not production workload guarantees.
