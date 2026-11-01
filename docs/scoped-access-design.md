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
`--reader-key-file /path/to/private-key` to expose the four read tools and
non-persistent update and translation proposal preparation tools;
the file must be owned by the process user and inaccessible to group/other.
Server authorization remains the enforcement boundary. The legacy reader
socket grants all sources by default to clients without a bearer key. Start
VEDA with `--reader-socket-require-key` to require scoped credentials on that
socket, and pass both `--reader-socket` and `--reader-key-file` to MCP. An
explicitly supplied invalid or revoked key never falls back to legacy implicit
socket access. The Linux root-only import path remains separate. This does not
isolate agents sharing an OS account: shell or filesystem access under that
account may allow an agent to read its other keys.

For an SSH-based stdio transport, `mcp/keyed_stdio_launcher.py --key-file KEY --
COMMAND...` sends the 64-byte key as a preamble to the trusted child and then
relays MCP input; the remote adapter uses `--reader-key-preamble` together with
`--reader-socket`. The key is not put in argv or environment. The child command
must be trusted and the local key file must be owner-only. This does not isolate
agents that can read each other's files under the same local OS account. An
existing unrestricted SSH/PVE administrator route also remains administrative
access regardless of the MCP key.

The scoped-key store uses format version 4 or newer so older binaries refuse it;
preserve the current store and verify backup compatibility before rollback.
The snapshot currently copies
authorized content per request; performance with large collections is unverified.
Keys have not been deployed or externally audited.
