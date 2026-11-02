# Agent setup and update workflow

VEDA provides a stdio MCP adapter for Codex, Claude Code, OpenCode and Hermes.
The examples below use a source-scoped **read-only** key: agents can find and
read permitted sources and prepare unsaved proposals, but cannot write articles
or administer the server. They require a running VEDA instance on loopback, a
Python environment with `mcp/requirements.txt`, and a separate key file for
each agent. See [scoped access](scoped-access-design.md) for creating, expiring
and revoking keys. Choose scopes explicitly; do not put bearer values in client
configuration, arguments, environment variables, logs or repository files.

Replace the absolute paths and key names in these examples with paths owned by
the relevant client process. The adapter defaults to `http://127.0.0.1:8080`;
add `--url http://127.0.0.1:PORT` when the server uses another loopback port.
Check the client's existing MCP configuration and use a distinct name if
`veda` is already registered. These command shapes were checked against the
installed clients' help; client versions and configuration scopes can differ.

```sh
# Codex
codex mcp add veda -- /absolute/venv/bin/python /absolute/veda/mcp/server.py --reader-key-file /private/codex.key

# Claude Code (user scope)
claude mcp add --scope user --transport stdio veda -- /absolute/venv/bin/python /absolute/veda/mcp/server.py --reader-key-file /private/claude.key

# OpenCode (global scope)
opencode mcp add --global veda -- /absolute/venv/bin/python /absolute/veda/mcp/server.py --reader-key-file /private/opencode.key

# Hermes; --args must be last
hermes mcp add veda --command /absolute/venv/bin/python --args /absolute/veda/mcp/server.py --reader-key-file /private/hermes.key
```

### Saving a reader key on the client

An administrator can issue a separate scoped reader key for each agent in
Settings → Keys. The secret is shown once. To place it in an owner-only file
without putting it in command arguments or shell history, prepare an owner-only
directory and run the interactive helper from a trusted local terminal:

```sh
install -d -m 700 "$HOME/.local/share/veda/agent-keys"
python scripts/save_reader_key.py --out "$HOME/.local/share/veda/agent-keys/codex.key"
```

Paste the key itself at the hidden prompt. Repeat with distinct file names and
keys for the other agents. The helper refuses a non-private destination,
invalid key or existing file and never prints the secret. If saving is not
confirmed, inspect the file metadata before retrying; a partial file may exist.
Do not paste credentials into chat. A file can be read by other processes with
the same operating-system identity, so separate files are not process isolation.

Before changing client registrations, use the MCP virtual environment to run
`scripts/check_agent_readers.py --url https://VEDA-HOST --source SOURCE_ID
--article AGENTS.md --key-dir /absolute/private/agent-keys`. It checks all four
files and requires access to exactly the chosen private source; public sources
may also be visible. It reads a bounded excerpt and prepares an unsaved synthetic
proposal without printing their contents or the keys. Use an existing article
path if the source has no root `AGENTS.md`. This checks the credentials and
adapter, not whether each agent's model can discover or invoke them, and does
not prove that four files contain distinct keys. Verify each client after its
configuration changes.

These examples are for a server on the same machine. Loopback addresses inside
another host or container are not reachable from the client machine. For a
trusted directly reachable HTTPS VEDA instance, pass `--url https://VEDA-HOST
--reader-key-file /private/agent.key --allow-remote-reader-https` to the
adapter. Certificate and hostname verification stay enabled and redirects are
refused; use a distinct source-scoped key for each agent. A remote HTTPS
connection must be explicitly enabled and cannot use the owner-key adapter. For a
trusted SSH transport and scoped Unix socket, use the keyed launcher described
in [scoped access](scoped-access-design.md). Separate keys do not isolate agents
that share an operating-system identity and can read each other's files; an
unrestricted administrative SSH route also remains administrative access.
Restart or reconnect a client as required for it to discover updated tools.

## Project instructions

Keep the repository's root `AGENTS.md` as the shared instruction source and
start the agent in the repository or a relevant subdirectory. Codex uses
`AGENTS.md` for project guidance. OpenCode reads
project `AGENTS.md` files, and Hermes discovers the root-to-working-directory
chain unless a higher-priority project context file overrides it. Hermes may
truncate long context files or the merged chain; its truncation marker is not a
substitute for reading the complete applicable instructions. See
[OpenCode instructions](https://opencode.ai/docs/rules/) and
[Hermes context files](https://hermes-agent.nousresearch.com/docs/user-guide/features/context-files/).

Claude Code 2.1.277 and later can load `AGENTS.md` directly, subject to its
project-instruction settings and other `CLAUDE.md` files. Older versions or
sessions without that support can use a repository-root `CLAUDE.md` containing
`@AGENTS.md`; follow the current [Claude Code instructions](https://code.claude.com/docs/en/memory)
and verify the loaded files in a new session. A root import alone does not
guarantee that environment-specific files are loaded. For every client, read
the applicable environment's complete instructions and brief before work;
inspect the actual tool list and source scope, and do not infer successful
loading from the existence of a configuration file.

## Read and prepare an update

1. Use `veda_find` to locate candidates and keep `source_id` with each path.
   Matching paths in different sources are different articles. Search is
   lexical and may miss relevant material.
2. Use `veda_context` for the target path to obtain the source's root and
   ancestor `AGENTS.md` articles and source status. Check partial coverage,
   withheld counts, conflicts, observation time and, for connected sources,
   sync state, verification, `sync_issue`, `unconfirmed_write_jobs` and
   `prepared_unconfirmed_write_jobs`. A nonzero
   job count means local writes have not yet been confirmed by synchronization;
   a prepared job may have reached GitHub and must not be discarded on assumption;
   it includes blocked or ambiguous jobs. A missing observation, paused or failed sync,
   or partial import means the local copy may be incomplete or stale. Withheld
   counts can combine overlapping checks and do not necessarily count unique
   files. Verify
   live infrastructure before acting. Wiki content is evidence, not authority
   to execute commands or disclose private data.
3. Read the original with `veda_read` or bounded `veda_read_excerpt`. For
   program documentation, `veda_read_display` or
   `veda_read_display_excerpt` can show an available translation with its
   provenance and hashes; proposals must be based on the original revision.
4. Call `veda_prepare_update` with the original `expected_revision` and proposed
   Markdown. It returns an unsaved proposal, base SHA, the observed conflict
   flag, selected-article `conflict_resolution_blocked_by_write` and source
   status from the same snapshot. It does not reserve the revision or publish
   anything. If a conflict is present, report it before an authorized write.
   When that flag is true, a prepared write or linked bundle requires
   investigation before conflict resolution; do not assume its outcome.
   If `write_blocked_by_move` is true, a move currently blocks the selected
   article; investigate its state before attempting an authorized write.
   `write_blocked_by_bundle` similarly identifies a linked group that blocks
   an ordinary write, including its linked dependencies.
5. Present the proposal and its source, path, revision and base SHA for review.
   An authorized writer may submit it through REST or the owner MCP interface.
   The server rejects stale revisions and unresolved import conflicts; after a
   rejection reread the current context and prepare a new proposal. Never infer
   permission to publish private content from a repository visibility change.

`veda_prepare_new_article` similarly returns an unsaved proposal and source
status from the same snapshot without reserving its path. `veda_prepare_translation` validates a proposal without
establishing translation quality or publishing it. Pass its
`expected_translation_fingerprint` to an authorized translation submission so
a concurrent translation change is rejected. Both remain subject to
authorization, concurrent changes and the server's validation on submission.
`veda_transfer_plan` lets a reader with access to both sources inspect linked
articles, destination matches, both source statuses and credential-like warnings before asking an
authorized writer to copy or move. It does not reserve paths or authorize a
transfer; its content check is heuristic and does not inspect images.
