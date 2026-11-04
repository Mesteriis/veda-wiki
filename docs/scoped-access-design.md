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
`--reader-key-file /path/to/private-key` to expose read and search tools and
non-persistent new-article, update and translation proposal preparation tools,
the transfer plan when both sources are authorized, and the translation queue
filtered to authorized sources;
the file must be owned by the process user and inaccessible to group/other.
Server authorization remains the enforcement boundary.

The local owner can create a separate key without printing it to a terminal:

```sh
./veda access-key create --id agent-one --sources infra --expires-at "$EXPIRY" --out /private/path/agent-one.key --key-file /private/path/owner.key
./veda access-key list --key-file /private/path/owner.key
./veda access-key revoke --id agent-one --key-file /private/path/owner.key
```

Set `EXPIRY` to an RFC3339 time within 366 days and choose an existing source ID. The output must not
already exist; the CLI creates it with mode 0600 and prints only metadata. On a
failed or uncertain request it keeps the reserved file: inspect the server's key
metadata before revoking or retrying. If the server accepted creation but the
file write failed, revoke that ID and protect the partial file. Revocation does
not remove local key files. The key file is usable by any process with its OS
owner's filesystem privileges.

The legacy reader
socket grants all sources by default to clients without a bearer key. Start
VEDA with `--reader-socket-require-key` to require scoped credentials on that
socket, and pass both `--reader-socket` and `--reader-key-file` to MCP. An
explicitly supplied invalid or revoked key never falls back to legacy implicit
socket access. The Linux root-only import path remains separate. This does not
isolate agents sharing an OS account: shell or filesystem access under that
account may allow an agent to read its other keys.

For a directly reachable private HTTPS listener, a read-only adapter may instead
use `--url https://VEDA-HOST --reader-key-file /private/agent.key
--allow-remote-reader-https`. This explicit option permits HTTPS only with a
reader-key file and a read-only tool set; the owner-key adapter remains
loopback-only. Standard certificate and hostname validation is mandatory; HTTP
proxies and redirects are disabled. Each agent should receive a separate,
source-scoped key. Configuring a remote URL grants that destination the key and
access to the requested documents, so use only a trusted VEDA endpoint. HTTPS
transport avoids relying on the administrative SSH route, but does not isolate
agents that share an operating-system account and can read each other's files.
An isolated real-backend smoke test exercises a trusted test certificate,
source-scoped read, unsaved proposal, out-of-scope denial and key revocation.
It does not establish that a real deployed agent has been provisioned or that
its TLS endpoint and credentials are configured correctly.

For an SSH-based stdio transport, `veda mcp-relay --key-file KEY --
COMMAND...` sends the 64-byte key as a preamble to the trusted child and then
relays MCP input; the remote `veda mcp` uses `--reader-key-preamble` together with
`--reader-socket`. The key is not put in argv or environment. The child command
must be trusted and the local key file must be owner-only. This does not isolate
agents that can read each other's files under the same local OS account. An
existing unrestricted SSH/PVE administrator route also remains administrative
access regardless of the MCP key.

The scoped-key store uses format version 4 or newer so older binaries refuse it;
preserve the current store and verify backup compatibility before rollback.
Each request snapshots authorized sources under the authorization lock, copying
mutable maps, slices and records while reusing immutable strings. A synthetic
large-document benchmark covers snapshot allocations, but production-scale
memory and concurrency still need validation.
Keys have not been deployed or externally audited.
