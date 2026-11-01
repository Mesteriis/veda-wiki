# Source-scoped agent access: work in progress

The current shared reader key can read every private source. A replacement must
enforce scope on the server for all content-bearing surfaces, including source
and article lists, search and rendered state, article reads and `AGENTS.md`
context, images, history and indirect metadata. Filtering controls in the UI
or MCP adapter is insufficient. Unauthorized objects should not be enumerable.

The initial key store creates independent, time-limited read scopes for named
sources. Bearer values are generated once and only their hashes are persisted;
revocation is durable. This is internal groundwork: keys are not yet exposed by
an API, accepted by the server or provisioned to agents. Existing owner, reader
and Unix socket behavior is unchanged.

Before enabling keys, implement and test the entire request path and all bulk
and indirect surfaces, credential administration and expiry, plus restart and
revocation. A key must never grant writes or owner-only metadata. Public and
private listeners must remain separate. The scoped-key store uses format version
4 so older binaries refuse it; coordinate this migration with the version-3
GitHub-source-move branch before integration, and verify backup and rollback.
