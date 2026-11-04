# Граф знаний в VEDA — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Автоматическое извлечение сущностей и отношений из видимых статей, производный граф в Neo4j, авторизованное чтение через REST/MCP/UI.

**Spec:** `docs/superpowers/specs/2026-11-04-knowledge-graph-design.md` (решения) и `docs/superpowers/specs/2026-11-04-knowledge-graph-brief.md` (требования владельца, авторитетны).

## Global Constraints

- Выключено по умолчанию: без `--knowledge` поведение, маршруты, MCP-набор и UI прежние (кроме неактивного переключателя, скрытого при `knowledge.enabled=false` в ответе статуса). `make test` не требует Python-модели, Docker и сети.
- Координаты — Unicode code points, полуоткрытые `[start, end)`, везде (Go, Python, JSON, UI).
- Снимок = `(source_id, path, content_sha256, pipeline_fingerprint)`. Номера ревизий не используются как идентичность.
- Доступ: разрешённый набор снимков строится на каждом запросе из request-scoped `domain.Database` с `public := !owner && !reader`; индекс получает его до обхода. Имена/алиасы/счётчики/ранжирование — только из разрешённых оснований.
- `Tx.AfterCommit` — только неблокирующий сигнал.
- Никаких записей из браузера (Origin-проверка существующая); owner-операции — только owner REST/MCP/CLI; reader/scoped key/reader socket — только чтение.
- Лимиты API: depth 1 (≤2), ≤200 узлов, ≤400 рёбер, `truncated`, пагинация, таймауты.
- Файлы ≤500 строк: Go, TS, CSS, Python (`check_file_size.py` расширяется на `services/**/*.py`); `scripts/check_architecture.py` знает новые пакеты.
- Закреплённые версии: `gliner2==2.1.0` (+ lock), Python 3.12, модель `fastino/gliner2.5-multi-v1@cf5593a5d45e3bbf204b9df621b13b1c0cf25ee3`, `neo4j:2026.10.1-community`, `github.com/neo4j/neo4j-go-driver/v6 v6.4.0` (если несовместимо — v5 + 5.26.x, с записью).
- Секреты (ключ извлекателя, пароль Neo4j) — только файлы 0600, флаги указывают путь; в логах и ответах — нет. Owner token не уходит извлекателю.
- Текст статей — недоверенные данные; цитаты в UI — текст, не HTML.
- Коммиты: каждая задача — один или несколько коммитов, каждый проходит `make lint` и `make test`.

## Review Focus

1. Скрытый документ влияет на публичный ответ через имя сущности, алиас, счётчик, ранжирование или промежуточный узел — тест в Task 5.
2. Медленная задача по старому SHA публикует результат после свежей — читатель не видит старые утверждения — тест в Task 3.
3. Опубликованная и текущая ревизии одной статьи дают разные графы для public/owner — тест в Task 5.
4. Длинная статья с кириллицей/emoji: цитаты совпадают с исходным Markdown по code points, хвост обработан, перекрытие не дублирует — тесты в Task 1 и Task 2.
5. Извлекатель/Neo4j недоступны: запись статьи, импорт и обычное чтение работают; статус knowledge — недоступен; после восстановления — догоняет — тест в Task 3.

---

### Task 1: Контракты, подготовка текста, политика доступа

**Files:** Create `internal/knowledge/{types.go,prepare.go,access.go,schema.go,resolve.go}` + тесты; `internal/knowledge/schema/v1.json` (схема сущностей/отношений, лимиты); `scripts/check_architecture.py` (новые пакеты и направления).

**Interfaces (Produces):**
- `type SnapshotKey struct{ SourceID, Path, SHA string }`; `type Fingerprint string`.
- `type Block struct{ ID string; Kind string; Text string; Map []Span }` — `Map` сопоставляет code-point диапазоны блока диапазонам исходного Markdown.
- `func Prepare(markdown string) ([]Block, error)` — абзацы/заголовки/элементы списков/ячейки таблиц; кодовые блоки, inline-код, HTML и frontmatter исключены (политика в комментарии и docs).
- `func SourceRange(b Block, start, end int) (srcStart, srcEnd int, exact bool)`.
- `func Allowed(db domain.Database, public bool, scope Scope, now time.Time) AllowedSet` — по правилам `ReadSnapshot`/`PublicRevision`/`SourcePublicReadable`; для владельца/читателя — текущая ревизия и опубликованная копия (если SHA отличается, только владельцу); `AllowedSet.Has(SnapshotKey)`, `Keys()`.
- `func ActiveSnapshots(db domain.Database) []ActiveSnapshot` — всё, что нужно проиндексировать: текущая и опубликованная (если отличается) у неудалённых статей.
- Модели ответа: `Entity`, `Mention`, `Claim`, `Evidence{SourceID, Path, SHA, Fingerprint, BlockID, Quote, Start, End, SourceStart, SourceEnd, Exact}`, `RelatedArticle{Key, Reasons []Reason}`, `Resolution{Method: "technical_key"|"alias"|"same_source_name"|"candidate"}`, `ClaimStatus: "extracted"|"uncertain"` с маркером.
- Схема v1 из spec (8 типов, 6 отношений с допустимыми концами); `func Fingerprint(schema, preprocessVersion, resolveVersion, checkpoint string) Fingerprint`.
- `func Normalize(entityType, name string) (key string, technical bool)`; правила по типам; без смешения письменностей.
- `func Markers(sentence string) []string` — версионированные ru/en маркеры отрицания/плана/примера/прошлого.

- [ ] Тесты: Prepare на кириллице, emoji, комбинируемых символах, повторных именах, таблице, списке, коде, frontmatter, длинной статье; SourceRange возвращает точные диапазоны; Allowed для public/owner/scoped key/team user/deleted/partial import/expired visibility proof/published≠current; одинаковые пути в двух источниках различимы; Normalize не склеивает «Сервис»/«Cepвиc» (смешанная письменность), person между источниками → кандидат.
- [ ] Commit: `"Add knowledge contracts, text preparation and access rules"`.

### Task 2: Python-извлекатель

**Files:** `services/knowledge-extractor/{pyproject.toml,uv.lock,README.md}`, `src/knowledge_extractor/{app.py,schemas.py,model.py,adapter.py,chunking.py,limits.py,config.py}`, `tests/`; `scripts/fetch_knowledge_model.py` (скачать ревизию в cache); `Makefile`: `make extractor-test` (unit с фейковой моделью, без сети) и подключение к `make test`; `check_file_size.py` на `services/**/*.py`.

**Interfaces:**
- `POST /v1/extract` (заголовок `X-Veda-Extractor-Key`), запрос: `{schema_version:1, request_id, schema:{entities:{type:desc}, relations:{name:{head:[types],tail:[types],desc}}}, blocks:[{id,text}], limits}`; ответ: `{schema_version:1, model:{id,revision,device}, blocks:[{id, chunks:[{start,end,feasible,status}], entities:[{local_id,type,start,end,text,confidence}], relations:[{type,head,tail,confidence?, head_confidence, tail_confidence}]}]}` — координаты в code points блока; `status: ok|infeasible|truncated_prevented`.
- `GET /health` (процесс), `GET /ready` (модель загружена, тестовый прогон на коротком тексте прошёл).
- Chunking: по токенизатору модели с учётом токенов схемы, окна ≤ max_len с запасом, перекрытие; дедупликация по (type, start, end) после пересчёта в координаты блока; хвост всегда покрыт.
- Путь извлечения: JointIE или `extract` со схемой — выбрать, проверив на реальной модели (`tests/test_real_model.py`, маркер `real_model`, запуск `make extractor-real`); документировать.
- Ограничения: размер тела, число блоков, длина блока, таймаут, семафор inference=1, один процесс.
- Устройство: `--device cpu|mps|cuda` (по умолчанию cpu); недоступное → ошибка ready с понятной причиной; без fallback молча.
- Офлайн: `HF_HUB_OFFLINE=1` + локальный cache; без cache — понятная ошибка ready.

- [ ] Unit-тесты с фейковой моделью: схемы, лимиты, ключ, chunking (кириллица, emoji, длинный текст, перекрытие без дублей, хвост), feasible=False, конверсия offsets Python→code points (Python str индексы = code points; проверить и зафиксировать).
- [ ] Реальная модель (отдельно): `make extractor-real` — загрузка, ready, ru/en пример, офлайн-запуск после fetch; результат в отчёте.
- [ ] Commit: `"Add the GLiNER2.5 knowledge extractor service"`.

### Task 3: Служебная БД, worker, reconciliation, клиент, memindex, wiring

**Files:** `internal/knowledge/jobs/` (SQLite: миграции, manifest, задачи, результаты), `internal/knowledge/extractorclient/`, `internal/knowledge/memindex/`, `internal/knowledge/{worker.go,reconcile.go,publish.go,service.go}`, `internal/app/run.go` (+ commands) флаги из spec, сигнал в store (`Store.OnCommit` или существующий хук; только неблокирующий send).

**Interfaces:**
- `type Extractor interface{ Extract(ctx, ExtractRequest) (ExtractResult, error); Ready(ctx) error }`.
- `type Index interface{ Publish(ctx, Snapshot, Graph) error; Retire(ctx, keys []SnapshotKey) error; Query...(ctx, AllowedSet, ...) ; Status(ctx) error }` — набор запросов чтения из Task 5 определяется здесь сигнатурами и реализуется memindex.
- Manifest: (key, fingerprint) → state pending/running/succeeded/failed/superseded, attempts, next_attempt, last_error_code; результат извлечения неизменяем по снимку+fingerprint.
- Reconcile: из `ActiveSnapshots` и manifest — новые/изменённые → pending; исчезнувшие/неактуальные → superseded + Retire; смена fingerprint → новая generation, чтение — только активной.
- Worker: N workers (по умолчанию 1), берёт текст копией из View, без lock вызывает extractor, проверяет актуальность снимка перед публикацией; retry с экспоненциальным backoff (до 6 попыток, потолок 30 мин); недоступность — статус `degraded`, без влияния на VEDA.
- Bounded shutdown через существующий контекст приложения.

- [ ] Тесты (fake extractor + memindex): обязательные случаи 1, 3, 4, 5, 6, 7 из brief §12; переполнение очереди не блокирует запись; crash-восстановление running → pending; повтор без дублей; смена fingerprint → переобработка; медленный старый worker не публикует поверх.
- [ ] Commit(s): `"Index knowledge in the background with reconciliation"`.

### Task 4: Neo4j-индекс

**Files:** `internal/knowledge/neo4jindex/` (driver v6), `deploy/knowledge/compose.yaml` (Neo4j Community + extractor CPU, закреплённые версии, loopback/внутренняя сеть, volumes вне Git), `scripts/knowledge_stack.py` (поднять/проверить/остановить dev-профиль).

- Модель: `(:Snapshot {key, sha, fp, generation, complete})`, `(:Mention)`, `(:Entity {id})`, `(:Claim {id, type, status})`, `(:Evidence)`; уникальные ограничения и индексы Community; публикация снимка — одна транзакция с `complete=true` в конце; Retire — удаление только evidence/claims/mentions этого снимка и сирот.
- Те же запросы чтения, что memindex, с разрешённым набором параметром (`UNWIND $allowed`), фильтрация до обхода.
- Паритет: `go test ./internal/knowledge/neo4jindex -tags neo4j` (или env `VEDA_TEST_NEO4J_URI`) гоняет общий набор контрактных тестов индекса против Neo4j; без env — пропуск с сообщением.

- [ ] Контрактные тесты индекса (общие для memindex и neo4j), включая Review Focus 1–3.
- [ ] Commit: `"Store the knowledge index in Neo4j"`.

### Task 5: Авторизованное чтение и REST

**Files:** `internal/knowledge/read.go` (сервис чтения: entities, entity, article, graph, context), `internal/httpapi/knowledge.go` (+ роли, маршруты), тесты.

- `GET /api/v1/knowledge/{entities,entity,article,graph,context}` — reader/owner/public по вычисленному режиму; `GET status`, `POST reindex` (202, `operation_id`) — только owner, POST по существующим правилам Origin; reader POST-исключение не расширяется.
- Контекст: лексический поиск (`internal/search`) + совпадения сущностей + обход ≤ depth; ответ с причинами отбора и `limit_bytes`.
- Состояние статьи: `disabled|processing|ready|stale|unavailable`.

- [ ] Тесты: обязательные случаи 2, 8, 9, 10, 11 (REST-часть) из brief §12 + Review Focus 1, 3; лимиты и `truncated`; неизвестные/недоступные id → 404.
- [ ] Commit: `"Serve authorized knowledge reads over REST"`.

### Task 6: MCP

**Files:** `internal/mcp/{tools.go,handlers_knowledge.go}`, `testdata/tools.json` (генерация), `scripts/check_agent_readers.py`, `scripts/smoke.py`, документация набора инструментов.

- Read: `veda_entities`, `veda_entity`, `veda_article_links`, `veda_graph`, `veda_graph_context`; owner: `veda_knowledge_status`, `veda_knowledge_reindex`. Через REST-клиент, строгие схемы, `ReadOnly`, `refuseUnknown`.
- Инструменты видны всегда (как остальные), при выключенном модуле возвращают ошибку `knowledge disabled`; golden-дифф — только добавления.

- [ ] Тесты: прямой вызов каждого tool, reader не может вызвать owner tools, неизвестные аргументы отклоняются; официальный SDK smoke.
- [ ] Commit: `"Expose knowledge reads to MCP agents"`.

### Task 7: Интерфейс

**Files:** `web/src/api/knowledge.ts` (+ типы), `web/src/features/knowledge/*`, `web/src/features/graph/*` (переключатель), `web/src/features/reading/*` (блоки «Сущности», «Связанные статьи»), CSS рядом, импорт в `main.tsx`; `scripts/ui_fixture.py`/`smoke_ui.mjs` при необходимости; скриншоты.

- Переключатель «Ссылки» / «Граф знаний» (`?graph=1&mode=knowledge`), поиск сущности, фильтры источника и типа отношения, центр, масштаб, карточка с документами, объяснением, цитатой и переходом; текстовый список связей; клавиатура; узкий экран; состояния disabled/processing/stale/unavailable/empty/loading; logout/смена сессии отменяют запросы и чистят данные.

- [ ] Тесты (Vitest + RTL): режимы, состояния, очистка при logout, поздний ответ не восстанавливает данные, цитата как текст; скриншоты существующих 27 экранов без изменений (модуль выключен), новые экраны — отдельный эталон.
- [ ] Commit: `"Show the knowledge graph in the reader"`.

### Task 8: Интеграция, оценка, документация

**Files:** `scripts/smoke_knowledge.py` (реальные GLiNER2.5 + Neo4j + VEDA REST + официальный MCP SDK на синтетических статьях; офлайн-прогон), `services/knowledge-extractor/eval/` (размеченный ru/en набор + скрипт оценки), `docs/knowledge-graph.md`, `README.md`, `docs/{architecture,api,agent-workflow}.md`.

- [ ] Smoke и оценка на реальной модели; замеры (время индексации, чтения графа, CPU/RAM) с условиями; MPS — нативный прогон, если работает.
- [ ] Документация: модель данных, доступ и SHA, ограничения модели, API/MCP, запуск, миграции, диагностика, переиндексация, офлайн, отключение.
- [ ] Commit: `"Document and verify the knowledge graph end to end"`.
