# Удаление перевода — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Убрать перевод из VEDA полностью: код, API, MCP, интерфейс, документацию, скрипты.

**Spec:** `docs/superpowers/specs/2026-11-04-remove-translation-design.md`

## Global Constraints

- Формат хранилища остаётся v21; старые ключи `translation`/`translation_fingerprint` в записях игнорируются при загрузке.
- Каждый коммит проходит `make lint` и `make test`; файлы ≤500 строк; границы пакетов `scripts/check_architecture.py`.
- Тесты, не связанные с переводом, не ослабляются; тесты, проверявшие только перевод, удаляются; смешанные тесты теряют только части про перевод.
- Тексты, классы и роли интерфейса вне меток перевода не меняются.
- `grep -rni 'translat\|перевод' internal cmd web/src scripts docs --exclude-dir=superpowers` в конце находит только исторические записи журнала (`docs/implementation.md`, если они описывают прошлое) — и каждое такое место перечислено в отчёте.

## Review Focus

1. Импорт, перенос, копирование и синхронизация статьи, у которой в базе остался старый ключ `translation` — работают, ключ пропадает после записи — тест в Task 1.
2. Бинарник `81dfeb2` открывает базу, записанную после удаления — `smoke_store_compat.py`/тест в Task 1.
3. Удалённые маршруты отвечают как неизвестные (404), а не 500 — тест в Task 1.
4. MCP перечисляет инструменты без пяти удалённых; остальные схемы байт в байт прежние — golden в Task 1.
5. Экран статьи программной документации без меток перевода, остальные экраны без изменений — скриншоты в Task 2.

---

### Task 1: Бэкенд и MCP

**Files:** удалить `internal/translation/`, `internal/httpapi/translation*.go`; изменить `internal/domain/{model,display,reading,moves,pull,records,prune,revision_assets}.go`, `internal/store/{validate,scoped}.go`, `internal/transfer/{copy,linked,move}.go`, `internal/syncer/pull.go`, `internal/importer/compare.go`, `internal/search/search.go`, `internal/httpapi/{articles,excerpts,data,roles}.go`, `internal/mcp/{tools,handlers_read,handlers_prepare,handlers_write}.go` + `testdata/tools.json`, `internal/app/run.go`, `scripts/check_architecture.py`; тесты соответственно.

- [ ] Тест: статья с записанным ключом `translation` (фикстура JSON и SQLite) загружается, читается, импортируется/переносится/копируется; после записи ключа нет.
- [ ] Тест: удалённые маршруты → 404; удалённые инструменты отсутствуют в `tools/list` владельца и читателя; golden обновлён генерацией, а не руками, и дифф golden содержит только удаление пяти инструментов и упоминаний перевода в описаниях оставшихся.
- [ ] Удалить код; `make lint`, `make test`, `python3 scripts/smoke_store_compat.py`.
- [ ] Commit: `"Remove translation from the backend and MCP"`.

### Task 2: Интерфейс

**Files:** `web/src/features/reading/Reading.tsx`, `web/src/lib/sourceWarnings.ts` (+тест), `web/src/api/types.ts`, `scripts/ui_fixture.py`, `scripts/smoke_ui.mjs`.

- [ ] Убрать метки и типы перевода; фикстура больше не сеет перевод; smoke не проверяет его.
- [ ] `npm run typecheck|lint|test|build`, `make build`, скриншоты: отличия только у экранов с бывшей меткой — перечислить их, пересъёмка эталона только этих экранов; `smoke_ui.py` PASS.
- [ ] Commit: `"Remove translation from the reading interface"`.

### Task 3: Скрипты и документация

**Files:** удалить `docs/translation.md`, `scripts/smoke_translation_auth.py` (и прочие скрипты только про перевод); изменить `scripts/{smoke,smoke_linux_arm64,smoke_public_github,check_agent_readers,package_release}.py`, `docs/{agent-workflow,api,architecture,content-model,github,implementation,import,linux-service,roadmap,scoped-access-design}.md`, `README.md`, CI workflow, если он запускает удалённые скрипты.

- [ ] Убрать упоминания; `docs/roadmap.md` — строка перевода удалена; `docs/implementation.md` — строка перевода удалена или помечена «удалено 2026-11-04».
- [ ] `make lint`, `make test`, затронутые скрипты запускаются (`--help` или синтетический прогон).
- [ ] Commit: `"Remove translation from scripts and documentation"`.
