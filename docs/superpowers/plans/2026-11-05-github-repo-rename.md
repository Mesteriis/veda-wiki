# Following GitHub repository renames — plan

**Goal:** a GitHub connection keeps synchronizing after its repository is renamed on
GitHub, and the owner can set the new name explicitly.

**Why:** `internal/github/client.go` refuses redirects on purpose; a renamed
repository answers 301 on the old name, which the client reports as
`upstream_rejected`, and no API changes `connection.Repository`. On 2026-11-05 a
rename of the owner's private knowledge repository stopped its import.

**Spec:** this document (the design below was agreed with the owner in chat).

## Design

1. `github.Client.call` maps 301/307/308 to `RemoteError{Code: "repository_moved"}`;
   still no redirect is followed.
2. `github.Client.RepositoryByID(ctx, id, token)` reads `GET /repositories/{id}`
   and validates the answer like `Repository` (ID must match).
3. `syncer.syncSource`: when `Repository` fails with `repository_moved`, resolve by
   `c.RepositoryID`; accept only the same ID and, for a private connection, a
   still-private repository; store the canonical new name with `ConfigRevision+1`,
   log one line, and continue the step with the new name. Otherwise keep the error.
4. `syncer.ConfigureRequest.Repository` (optional): canonicalized, verified through
   `Repository` with the connection's token; same `RepositoryID` required, else
   `ErrConflict`; applied in the same update as account/write changes.
5. CLI `veda github configure --repository`; docs in `docs/github.md`, `docs/api.md`.
   GitHub App installation tokens are scoped by repository name, so an App
   connection cannot resolve the rename itself: the owner sets the name.

## Tasks (TDD)

- Task 1 — client: tests `TestCallReportsRepositoryMoved`, `TestRepositoryByID`
  (happy path, ID mismatch → `invalid_upstream_response`).
- Task 2 — fake server: `githubtest.Fake.Renamed` (old name → 301) and
  `/repositories/{id}`.
- Task 3 — syncer follow: `TestSyncFollowsRenamedRepository`,
  `TestSyncKeepsErrorWhenRenamedRepositoryDiffers`.
- Task 4 — configure: `TestConfigureSetsRepositoryName`,
  `TestConfigureRejectsRepositoryWithOtherID`.
- Task 5 — CLI flag, docs, `make lint`, `make test`, review, PR.
