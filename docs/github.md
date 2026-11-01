# GitHub synchronization

GitHub connections are owner-only. Each connection has a stable source ID,
one repository and branch, and either anonymous access or an explicitly
registered account. Anonymous connections are read-only. The server verifies
repository identity, visibility and branch before connecting. A wiki has root
`veda.yaml`; otherwise only `docs/` is imported. Missing `docs/` is skipped.

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
token account registration and repository connection after a separate
administrator login.
The administrator session grants GitHub account, connection and manual sync
endpoints, not article editing or the general owner API. It is accepted only over direct
HTTPS or loopback HTTP, and is separate from the reader session. The read-only
Unix socket does not expose administration; an independently configured local
bridge can forward only the protected administration routes.

The worker polls every five minutes by default (`--sync-interval`, 5 seconds
to 1 hour). Local writes to connected sources are committed with an outbox
entry, and pushed as non-force commits after pulling and conflict checks. The
GitHub reference update requires the exact previously observed branch head;
an intervening branch rewind is rejected as well as an ordinary concurrent push.
If its response is uncertain, a subsequent complete pull resolves the outbox.
Transferred relative images are committed with their article in the same Git
commit. An existing image must match, and a partial import or repository size
limit blocks the bundle. A complete pull must observe every image before the
move removes its source. Previously unapproved private images remain private
when a queued article is edited.
Status, retry and pause are available through the CLI and owner REST API.
An administrator can also schedule an immediate retry from the GitHub
connection screen. It may push already queued changes when write access is enabled;
paused connections must first be resumed through the owner API or CLI.
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
