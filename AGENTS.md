# VEDA engineering

React is a reading interface. Authors use REST, MCP or repository synchronization.
Keep article/source operations in the shared Go service; MCP is an adapter.
Use stable source IDs: identical article paths in different sources are distinct.
Wiki sources will use root veda.yaml metadata; program sources use docs/ only.
Repository visibility determines publication, never a folder name or YAML flag.

Default listeners are loopback. Never expose owner credentials or private data
in responses, logs, screenshots, examples or Git. Do not disable Host checks.
The public listener must ignore owner tokens. Browser writes are disabled.
Keep state/key files and local machine configuration out of the repository.

Run make test after meaningful backend changes; use the official SDK smoke
test for MCP changes. Never claim GitHub synchronization, translations, user
accounts or cross-repository moves work until implemented and tested.
