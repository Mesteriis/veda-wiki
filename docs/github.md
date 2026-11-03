# GitHub synchronization

GitHub connections are owner-only. Each connection has a stable source ID,
one repository and branch, and either anonymous access or an explicitly
registered account. The repository accepts `owner/repository` or an HTTPS
`github.com/owner/repository` link, with optional `.git` and trailing slash. Anonymous connections are read-only. The server verifies
repository identity, visibility and branch before connecting. A wiki has root
`veda.yaml`; otherwise only `docs/` is imported. Missing `docs/` is skipped.
For a skipped repository with no local source, VEDA remembers the checked Git
head. An unchanged head avoids downloading the tree again, but repository
visibility and the branch head are still checked on each sync. A new commit or
a visibility change triggers a fresh tree inspection.

An opt-in anonymous acceptance check imports the public VEDA program repository
into a disposable local store, verifies `docs/`, checks one article against the
recorded GitHub commit, and restarts the store. It makes no GitHub writes and
does not use stored credentials:

```sh
VEDA_TEST_PUBLIC_GITHUB=1 .venv/bin/python scripts/smoke_public_github.py
```

This check requires outbound GitHub access and available anonymous API quota.
It does not verify GitHub App or token authentication, private repositories,
write-back, translation quality, or the connectivity of another host.

For two distinct public program repositories, the optional multi-source check
imports both into a disposable store, migrates it to SQLite, restarts, and uses
the official MCP SDK to search, read and prepare unsaved proposals. It checks
the anonymous rate limit before starting and skips if fewer than 40 requests
remain. Repository sizes and upstream changes can still exceed that budget.
It makes no GitHub writes and does not verify private access. An equivalent
check on the deployment host passed for two public program repositories,
including SQLite restart and source-filtered MCP search, read and proposals:

```sh
VEDA_TEST_PUBLIC_MULTI=1 VEDA_TEST_PUBLIC_REPO_ONE=OWNER/REPO1 \
  VEDA_TEST_PUBLIC_REPO_TWO=OWNER/REPO2 .venv/bin/python scripts/smoke_public_multisource.py
```

A separate opt-in check anonymously clones two public program repositories
into temporary directories and imports the checkouts as explicitly public local
sources. It compares one article per source byte-for-byte, migrates to SQLite,
restarts, and exercises merged search, reading and proposals over MCP. This
check does not use the GitHub API and does not establish repository visibility
or test GitHub connections, credentials, background synchronization or writes.

```sh
VEDA_TEST_PUBLIC_CHECKOUT_MULTI=1 VEDA_TEST_PUBLIC_REPO_ONE=OWNER/REPO1 \
  VEDA_TEST_PUBLIC_REPO_TWO=OWNER/REPO2 .venv/bin/python scripts/smoke_public_checkout_multisource.py
```

A separate opt-in check clones the existing synthetic public wiki fixture branch
and an explicitly supplied public program repository. It verifies `veda.yaml`,
an image, `docs/` boundaries, exact article bytes, SQLite restart and MCP
reads/proposals. It makes no GitHub writes and does not exercise the GitHub API;
the existing fixture branch must remain available:

```sh
VEDA_TEST_PUBLIC_WIKI_PROGRAM=1 VEDA_TEST_PUBLIC_PROGRAM_REPO=OWNER/REPO \
  .venv/bin/python scripts/smoke_public_wiki_program_checkout.py
```

An opt-in read-only GitHub API check imports that public wiki fixture and the
public `Mesteriis/mac-tower` program repository into a disposable store. It
verifies repository type, wiki metadata and image, program `docs/` boundaries,
SQLite restart, a locally simulated outage and recovery of one source, and
scoped MCP search, reads and unsaved proposals. During that simulated outage
the other source remains available and the local wiki revision is retained.
`gh` must
already have Mesteriis selected; a local GET-only proxy uses its credential
without passing a token to VEDA. This check performs no GitHub writes and does
not establish that VEDA-stored credentials, private repositories, write-back or
the deployment host's GitHub connectivity work. A separate anonymous test on
the deployment host passed the same two repository types, metadata and image,
`docs/` boundaries, SQLite restart and MCP search/read/proposals. It did not
test outage recovery or credentials on that host:

```sh
cd server
VEDA_LIVE_GITHUB_MIXED=1 VEDA_TEST_MCP_PYTHON=/path/to/python \
  go test -count=1 -run '^TestLiveGitHubMixedSourcesReadOnly$' -v .
```

The local integration test uses two fake GitHub repositories, including one
private source, and the MCP SDK to verify merged search, source-scoped reads,
proposals and denial after SQLite restart. It also verifies that a local write
and its unconfirmed job remain visible through scoped MCP context and proposals
after SQLite is reopened; the other source reports zero jobs. Run it with a
Python environment that has the MCP SDK:

```sh
cd server && VEDA_TEST_MCP_PYTHON=/path/to/python go test -run '^TestMultiSourceMCPAfterSQLiteRestart$' -v .
```

An opt-in browser acceptance test connects one explicitly chosen public program
repository through the GitHub administration dialog in a disposable VEDA store.
It verifies the connection is anonymous and read-only, waits for synchronization,
checks that only `docs/` articles appear, and opens the article list. It requires
Node 22+, a trusted Chrome or Chromium binary and at least 20 anonymous GitHub
API requests remaining; larger repositories may need more. It does not run in
CI or test saved credentials, private access, write-back, translation quality,
or connectivity from another host. The public `Mesteriis/mac-tower` repository
passed this check on a workstation with six articles on 2026-11-02:

```sh
make build
VEDA_TEST_GITHUB_BROWSER=1 VEDA_TEST_CHROME=/absolute/path/to/chrome \
  VEDA_TEST_PUBLIC_PROGRAM_REPO=OWNER/REPOSITORY python3 scripts/smoke_public_github_browser.py
```

For a public repository, start VEDA and connect it without credentials:

```sh
./veda github connect --id handbook --repository OWNER/REPOSITORY
./veda github status
./veda github sync --id handbook
```

Private access and optional write access require an explicit account. Register
a GitHub App installation or a token through the owner API, or with
`./veda github account --id ACCOUNT --secret-file PATH`. The input must be a
regular file inaccessible to group/others. JSON fields for a token are `kind`
(`token`) and `token`; for an App they are `kind` (`app`), `app_id`,
`installation_id` and `private_key` (PEM string). Protect and remove the input
file after registration. VEDA stores credentials separately in its private data
directory; account listings do not return them. Never commit credentials or
pass them on a command line. Connect using `--account ACCOUNT`; add `--write`
only when the account has the required repository permission. No account is
automatically guessed. The private browser interface also offers GitHub App or
token account registration and repository connection. An already signed-in
named administrator can use this dialog without another login.
The connection form derives a source ID from the repository address and
selects an existing account when there is exactly one; the owner can choose
another account or set an explicit source ID in advanced settings.
The administrator session grants GitHub account, connection, configuration,
manual sync and pause/resume endpoints, not article editing or the general owner API. It is accepted only over direct
HTTPS or loopback HTTP. A reader session grants these permissions only to a
currently enabled named administrator, not ordinary users or reader keys. The read-only
Unix socket does not expose administration; an independently configured local
bridge can forward only the protected administration routes.
The connection dialog can change the selected account and write permission
using the current configuration revision. A concurrent change is rejected;
reload and review the updated settings before retrying. Repository and branch
identity are fixed for an existing connection. A same-account configuration
change does not clear an active GitHub rate-limit cooldown.

The worker polls every five minutes by default (`--sync-interval`, 5 seconds
to 1 hour). Local writes to connected sources are committed with an outbox
entry, and pushed as non-force commits after pulling and conflict checks. The
GitHub reference update requires the exact previously observed branch head;
an intervening branch rewind is rejected as well as an ordinary concurrent push.
If its response is uncertain, a subsequent complete pull can confirm the write
when it observes the expected content and dependencies. A later local edit does
not discard that prepared job or overtake it on the same path. If the upstream
content instead conflicts, VEDA retains both the conflict and the uncertain
job; conflict resolution is refused until the prepared outcome is reconciled.
An external change matching a later edit cannot silently close a different
earlier prepared write. If a verified pull later observes the prepared content
and its dependencies, VEDA can confirm it even after a conflict; identical
later unprepared edits are satisfied by the same observation without a new
commit.
There is currently no automatic resolution for every divergent or ambiguous
history; an administrator must investigate rather than discard the job.
Jobs already marked superseded by an older VEDA version are not automatically
restored or treated as proof of a remote outcome.
Transferred relative images are committed with their article in the same Git
commit. An existing image must match, and a partial import or repository size
limit blocks the bundle. A complete pull must observe every image before the
move removes its source. Previously unapproved private images remain private
when a queued article is edited.
Status, retry and pause are available through the CLI and owner REST API.
An administrator can schedule an immediate retry or pause and resume from the
GitHub connection screen. Resuming may push already queued changes when write
access is enabled. Pausing hides that source's public articles while its GitHub
visibility is not being verified; a network request already in flight may finish.
The server rejects sync status and visibility observations made using an older
connection configuration, including after pause and resume. A fresh verification
is required before public content reappears. While synchronization checks the
remote head, public readers keep the previously imported snapshot only within
its existing ten-minute visibility proof; the check at the start of a sync does
not extend that proof, and a repository that has become private is hidden
immediately. Once a sync observes a different remote head, the previous public
snapshot is withheld until that head is imported; private readers retain their
local copy. The proof is renewed after the pull completes. If an imported
repository changes type,
or a wiki loses `veda.yaml` and has no `docs/`, synchronization reports
`repository_type_changed` and keeps its local history. Restore the original
repository structure and retry to resume. Automatic source type conversion is
not supported.
An invalid or unsupported root `veda.yaml` reports `invalid_veda_metadata`;
a missing or unsupported referenced cover reports `veda_image_unavailable`.
In either case VEDA keeps the local snapshot and hides an unverified public
snapshot. Correct the file or image and retry; diagnostic codes do not include
the file contents or image path.
Outbox status changes also require the same connection revision and an unchanged
job identity and visibility. A prepared commit is retained for recovery if the
connection changes during an uncertain remote write.
The reading interface refreshes its state periodically and when the dialog is
opened or closed, so imported pages appear without a browser reload. Anonymous
GitHub API limits can delay verification; while verification is unavailable,
public visibility fails closed until a successful retry. The connection screen
marks visibility unconfirmed when its latest proof is stale, paused or failed;
its age indicator uses the browser clock and is informational. The server
independently enforces visibility using its own clock.
If a connected public repository yields an incomplete import (for example an
invalid or withheld file), public readers cannot access that source until a
complete snapshot has been imported. Private readers retain the local data.
When a complete public snapshot confirms an upstream deletion, its old imported
publication is withdrawn even if a conflicting local draft must be retained.
When GitHub reports a rate limit, VEDA persists the source's next retry time,
honoring `Retry-After` and the primary rate-limit reset with a one-minute
minimum and a 24-hour cap. Manual retry cannot bypass this deadline. The same
cooldown is applied to existing connections using the same VEDA account, or to
all existing anonymous connections; it is persisted through restarts. Separate
VEDA account IDs may still share a GitHub limit, and account registration or a
new connection is not blocked by this background cooldown. A 403 without
rate-limit headers is treated as a permission failure. Shared cooldown also
clears public visibility verification for those connections until their next
successful synchronization; their private local copies remain available.
Conflicts retain both versions. The worker checks visibility before import and
private writes; public reads fail closed if the visibility proof is older than
ten minutes, unavailable or paused. Visibility may change externally between
checks; GitHub does not provide an atomic visibility-and-write operation.

If a repository becomes public, a previously queued private edit is blocked.
After reviewing the exact content, the owner can approve its job using
`POST /api/v1/github/publish-job` with `job_id`, `expected_revision` and
`confirm_publication: true`, or the corresponding MCP tool. The operation
publishes the approved revision and permits its background push, including
any queued image dependencies. Review the article and those images before
approving. Never approve
it based solely on a visibility change. Public Git history cannot be recalled.

A subprocess test also starts the synchronization logic in a fresh test
process after a simulated lost push response and SQLite migration. It verifies
that the already applied commit is acknowledged without a duplicate push. The
GitHub service and credentials in that test are synthetic; it does not launch
the production server binary or prove real credential recovery.
The same fresh-process check covers a v17 move with a directory index mapped
to a README: the source remains until the mapped bundle is observed, and the
linked source document is retained after the move completes.

Automated coverage uses a simulated GitHub service; anonymous public import has
also been checked against a real public repository. Real authenticated App/PAT
connections, writes and multi-repository recovery still require acceptance.
Cross-repository moves have simulated-GitHub coverage and an opt-in live
synthetic check using a `gh` credential-owning proxy; authentication using
VEDA's own saved App/PAT credential still needs live acceptance. An optional local translation
worker and source-scoped read-only keys are implemented; model qualification
and production agent-key rollout remain outstanding. See
[translation](translation.md) and [scoped access](scoped-access-design.md).

An opt-in live acceptance test exercises a move between two branches of the
public `Mesteriis/veda-wiki` repository using synthetic content, images and a
linked article. It checks that the source remains until the destination group
has been observed, the linked source article remains, and the final remote
tree and destination blobs match. The local test store receives a
synthetic token; a loopback test proxy invokes the authenticated `gh` CLI,
so it does not test VEDA's handling of a real PAT or App installation. With `gh`
already authenticated as Mesteriis, run:

```sh
cd server
VEDA_LIVE_GITHUB_MOVE=1 go test -count=1 -run '^TestLiveGitHubBranchMove$' -v .
```

Add `VEDA_LIVE_GITHUB_DIRECTORY=1` to include a directory `index.md` mapped
to a destination `README.md`. This variant passed on 2026-11-02 between two
new branches of the public VEDA repository, including image and linked-article
checks. It used the same `gh` proxy and synthetic VEDA credential; it did not
exercise saved credentials or a private repository.

Add `VEDA_LIVE_GITHUB_RECOVERY=1` to the same command to create a synthetic
article through the REST handler, simulate an upstream outage, migrate and
reopen the local store as SQLite, and verify that the queued write reaches the
source branch and is observed after recovery before continuing the move test.
With an explicit `VEDA_LIVE_GITHUB_TARGET=Mesteriis/mac-tower`, this passed on
2026-11-02 across two public repositories. The test uses a `gh` proxy and a
synthetic VEDA token; it does not test VEDA-stored credentials, a full process
restart, private repositories, or the deployment host's connectivity.

Each run creates two public fixture branches and retains them for inspection;
the test does not change an existing branch or use production wiki data.
Setting `VEDA_LIVE_GITHUB_TARGET=Mesteriis/mac-tower` explicitly runs the
same test across two repositories. The target fixture starts from the other
repository's default branch and preserves its existing files; only the new
branch gets synthetic metadata and the moved fixture. On 2026-11-01 the
two-repository test passed against public GitHub with the linked article absent
from the target before transfer, via the `gh` proxy. The test refuses
to create the target branch if its source tree contains GitHub Actions
workflows, avoiding an unintended workflow run.

### Attaching an existing private source

An owner may explicitly attach a disconnected private source to a private GitHub
repository with `adopt_existing: true` on `POST /api/v1/github/connections`, or
`github connect --adopt-existing`, using the existing source ID and an explicitly
selected account. The admin interface offers the same option. The repository
must have the same source type, a complete import without withheld files, and
all existing source assets must be present with identical contents. Tombstones,
unresolved conflicts, pending operations, or a concurrent source change reject
the attachment. Repository identity, privacy and branch head are checked again
before the connection and import are committed together.
For a verified private repository, article and image bytes from disconnected
private sources can be reused when their path, size and Git object digest match
the authenticated repository tree. Source identities remain separate. Other
content is fetched and checked normally. During attachment, the complete local
source is checked again before committing, including when bytes were reused.

Matching articles retain their revision history. Different versions become
conflicts, repository-only articles are imported, and local-only articles remain
local; attachment does not queue them for upload. Resolve conflicts and review
local-only material before any subsequent write. A source configured with `--local-import-source` cannot be attached: first
retire or reconfigure that importer, because it cannot update a connected source.
Adoption has a five-minute server deadline; client or network interruption can
leave the response uncertain, so inspect connection status before retrying.
