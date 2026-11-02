# Roadmap

- [x] Standalone React/Go application with empty local storage.
- [x] Distinct local sources and public/private read isolation.
- [x] Durable article revisions and optimistic write concurrency.
- [x] Authenticated REST create/read/update and MCP adapter using the same API.
- [x] Agent context from root/ancestor AGENTS.md articles.
- [x] Versioned veda.yaml parser and bounded local-checkout import.
- [x] Manual upstream conflicts, deletion handling and revision-checked resolution.
- [x] Confined, visibility-checked local image import and serving.
- [ ] GitHub connection, repository visibility and credential management: implemented; live authenticated acceptance pending.
- [ ] Durable background import/push outbox: implemented and mock-tested; live acceptance pending.
- [ ] Program docs/ import and provenance-preserving Russian translation.
- [ ] Stable cross-source article moves and publication review.
- [ ] User/team permissions and private browser reading sessions.
- [ ] SQLite migration and retention: opt-in backend deployed; production restore boot, indexing and retention remain pending.
- [ ] Packaging, deployment hardening and independent restore acceptance.

Full-scope status is tracked in [implementation ledger](implementation.md).
