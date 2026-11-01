# Source-scoped agent access: work in progress

The current shared reader key can read every private source. A replacement must
enforce scope on the server for all content-bearing surfaces, including source
and article lists, search and rendered state, article reads and `AGENTS.md`
context, images, history and indirect metadata. Filtering controls in the UI
or MCP adapter is insufficient. Unauthorized objects should not be enumerable.

The key store creates independent, time-limited read scopes for named sources.
Bearer values are generated once and only their hashes are persisted; revocation
is durable. An owner can create, list metadata and revoke keys over REST. The
server builds a request-local snapshot of only authorized sources before serving
reader routes, including search, context and media; writes and owner metadata
remain unavailable. Requests already in progress may finish after revocation.
Existing owner, shared reader and Unix socket behavior remains unchanged. Keys
have not yet been provisioned to the four agents. Use MCP
`--reader-key-file /path/to/private-key` to expose only the four read tools;
the file must be owned by the process user and inaccessible to group/other.
Server authorization remains the enforcement boundary. The legacy reader
socket grants all sources and is not restricted by scoped keys.

The scoped-key store uses format version 4 so older binaries refuse it;
coordinate this migration with the version-3 GitHub-source-move branch before
integration, and verify backup and rollback. The snapshot currently copies
authorized content per request; performance with large collections is unverified.
Keys have not been deployed or externally audited.
