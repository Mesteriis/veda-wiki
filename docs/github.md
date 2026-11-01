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
automatically guessed. There is currently no browser Connect flow.

The worker polls every five minutes by default (`--sync-interval`, 5 seconds
to 1 hour). Local writes to connected sources are committed with an outbox
entry, and pushed as non-force commits after pulling and conflict checks. The
GitHub reference update requires the exact previously observed branch head;
an intervening branch rewind is rejected as well as an ordinary concurrent push.
If its response is uncertain, a subsequent complete pull resolves the outbox.
Status, retry and pause are available through the CLI and owner REST API.
Conflicts retain both versions. The worker checks visibility before import and
private writes; public reads fail closed if the visibility proof is older than
ten minutes, unavailable or paused. Visibility may change externally between
checks; GitHub does not provide an atomic visibility-and-write operation.

If a repository becomes public, a previously queued private edit is blocked.
After reviewing the exact content, the owner can approve its job using
`POST /api/v1/github/publish-job` with `job_id`, `expected_revision` and
`confirm_publication: true`, or the corresponding MCP tool. The operation
publishes the approved revision and permits its background push. Never approve
it based solely on a visibility change. Public Git history cannot be recalled.

Automated coverage uses a simulated GitHub service; anonymous public import has
also been checked against a real public repository. Real authenticated App/PAT
connections, writes and multi-repository recovery still require acceptance.
Cross-repository moves have simulated-GitHub coverage but have not been
accepted against authenticated live repositories. An optional local translation
worker and source-scoped read-only keys are implemented; model qualification
and production agent-key rollout remain outstanding. See
[translation](translation.md) and [scoped access](scoped-access-design.md).

An opt-in live acceptance test exercises a move between two branches of the
public `Mesteriis/veda-wiki` repository using synthetic content. It checks that
the source remains until the destination has been observed, and checks the
final remote tree and destination blob. The local test store receives a
synthetic token; a loopback test proxy invokes the authenticated `gh` CLI,
so it does not test VEDA's handling of a real PAT or App installation. It also
does not establish behavior across two distinct repositories. With `gh`
already authenticated as Mesteriis, run:

```sh
cd server
VEDA_LIVE_GITHUB_MOVE=1 go test -count=1 -run '^TestLiveGitHubBranchMove$' -v .
```

Each run creates two public fixture branches and retains them for inspection;
the test does not change an existing branch or use production wiki data.
