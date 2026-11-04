# Хранилище: ревизии отдельными записями, картинки по ревизиям, чистка истории — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Хранить у статьи 3 последние ревизии (плюс защищённые), каждую ревизию — отдельной строкой SQLite со своим набором картинок, удалять неиспользуемые блобы и переносить LXC161 офлайн-командой с копией прежней базы.

**Architecture:** Правила (номер текущей ревизии, защита, чистка, набор картинок ревизии) — чистые функции в `internal/domain`. `internal/store` применяет чистку при каждой фиксации, хранит ревизии в таблице `revisions`, собирает мусорные блобы и выполняет перенос. Модель `domain.Database` в памяти и контракт `View/Update` для остальных пакетов не меняются. Вызывающие пакеты переходят с `len(Versions)` и `Versions[n-1]` на функции домена.

**Tech Stack:** Go 1.26, SQLite (текущий драйвер проекта), goldmark; Python-скрипты проверок.

**Spec:** `docs/superpowers/specs/2026-11-04-storage-revisions-design.md`

## Global Constraints

- Хранится 3 последние ревизии статьи: `domain.KeepRevisions = 3`, без настройки.
- Номера ревизий монотонны и не переиспользуются; «текущая ревизия» = номер последней ревизии, `expected_revision` сравнивается с ним.
- Логический формат v21 (`db.Version`); маркер бэкенда SQLite остаётся `{"version":14,"backend":"sqlite"}`. Бинарник с этим изменением не открывает базу ниже v21: ошибка `store format vN requires "veda migrate-revisions"`; новые пустые базы создаются сразу в v21.
- Блоб удаляется, только если на него нет ни одной ссылки в базе и время изменения файла старше 24 часов; сборка под блокировкой записи хранилища.
- Команды и логи печатают только количества: никаких путей статей, текстов, имён картинок, ключей.
- Правила `AGENTS.md`: слушатели на loopback, проверка Host не отключается, публичный слушатель игнорирует токены владельца, записи из браузера нет.
- Каждый коммит проходит `make test`; `make lint` (≤500 строк на файл, `scripts/check_architecture.py`, staticcheck, gofmt). Домен без ввода-вывода.
- Тексты ошибок REST/MCP и коды ответов не меняются, кроме отказа открыть базу ниже v21.
- Развёртывание на LXC161 в план не входит (отдельное действие с согласия владельца).

## Review Focus

1. Ревизия была защищена (задание очереди, перенос), защита снялась — при следующей записи статьи или полной фиксации она удаляется; до этого очередь и перенос читают её текст — тест в Task 2 и Task 4.
2. Статья с ревизиями 7, 8, 9: `PUT` с `expected_revision: 9` проходит, с `3` — конфликт; `?history=1` возвращает 7–9 — тест в Task 1 и Task 4.
3. Старый блоб без ссылок старше суток заново нужен параллельной правке: `WriteBlobs` до фиксации, сборка между ними — блоб не удалён — тест в Task 6.
4. Публичный слушатель, `/media/<src>/<path>?v=<sha>`, где `sha` есть только у неопубликованной ревизии или у приватного источника — 404 — тест в Task 3.
5. Прерванный или повторный `migrate-revisions` — отказ с понятным сообщением, копия прежней базы на месте, база открывается прежним путём восстановления — тест в Task 7.

---

### Task 1: Номера ревизий через функции домена

**Files:**
- Create: `internal/domain/revisions.go`, `internal/domain/revisions_test.go`
- Modify: все места `grep -rn '\.Versions' internal` вне тестов (около 30 файлов: `domain/{articles,reading,moves,pull}.go`, `syncer/{push,publish,reconcile,moves,pull,state}.go`, `transfer/{plan,copy,move,linked,assets}.go`, `importer/{conflicts,apply}.go`, `translation/{worker,submit,validate}.go`, `agentctx/{proposal,context}.go`, `search/search.go`, `httpapi/{articles,excerpts}.go`, `store/{validate,tx,scoped}.go`)

**Interfaces:**
- Produces: `func LatestRevision(a Article) int` (0 для пустой), `func RevisionByNumber(a Article, n int) (Revision, bool)`, `func LastRevision(a Article) (Revision, bool)`.

- [ ] **Step 1: Тесты** `revisions_test.go`: статья с ревизиями `7,8,9` → `LatestRevision == 9`, `RevisionByNumber(8)` найдена, `RevisionByNumber(3)` нет; пустая статья → 0. `PutArticle(..., expected 9)` у такой статьи создаёт ревизию 10, `expected 3` → `ErrConflict`. `store.Validate`: номера `7,8,9` с верными SHA — ок; `7,9,8` или повтор — ошибка.
- [ ] **Step 2:** Run `go test ./internal/domain/ ./internal/store/` — FAIL.
- [ ] **Step 3:** Реализовать функции; заменить каждое `len(x.Versions)` в смысле «номер текущей ревизии» на `LatestRevision`, каждое `Versions[n-1]` на `RevisionByNumber`, `Versions[len-1]` на `LastRevision`. `Validate`: строгое возрастание номеров (≥1) вместо `Number == i+1`; ссылки `ReconcileBase`/`Move` ищутся по номеру. Поведение для непрерывной истории не меняется.
- [ ] **Step 4:** `make test` — PASS. Commit: `"Address revisions by number instead of position"`.

### Task 2: Правило чистки и защиты

**Files:**
- Create: `internal/domain/prune.go`, `internal/domain/prune_test.go`
- Modify: `internal/store/validate.go` (проверка переносов)

**Interfaces:**
- Consumes: Task 1.
- Produces: `const KeepRevisions = 3`; `func ProtectedRevisions(db Database, key string) map[int]bool`; `func PruneArticle(db *Database, key string) (removed int)`; `func PruneAll(db *Database) (removed int)`.

Защищены ревизии статьи `key`, на которые ссылаются: незавершённое задание `Outbox` (`SourceID/Path/Revision`, состояние не завершённое), незавершённый `Move` (`SourceRevision`; состояния `complete`, `cancelled`, `source_changed`, `destination_changed` — завершённые), `ReconcileBase` любой другой статьи, незавершённый перевод, читающий ревизию. Исполнитель проходит вызовы `RevisionByNumber` из Task 1 и добавляет в `ProtectedRevisions` каждую найденную зависимость, читающую текст не последней ревизии; список фиксируется в комментарии функции. `Published` не защищает (это копия). `CopyOrigin`, `ImportConflict.BaseSHA`, `UpstreamHash` не защищают.

- [ ] **Step 1: Тесты** `prune_test.go`: 5 ревизий → остаются 3 последние; 2 ревизии → без изменений; защита заданием очереди на ревизию 1 из 6 → остаются 1,4,5,6; задание стало завершённым → после `PruneArticle` остаются 4,5,6; то же для незавершённого переноса и `ReconcileBase`; удалённая статья (`Deleted`) — то же правило; `Published` с текстом ревизии 1 переживает чистку ревизии 1. `store.Validate`: завершённый перенос, чья исходная ревизия удалена, — валиден; незавершённый без своей ревизии — ошибка.
- [ ] **Step 2:** Run — FAIL → реализовать → PASS. `make test`, `make lint`.
- [ ] **Step 3:** Commit: `"Add the revision retention rule and its protections"`.

### Task 3: Картинки по ревизиям

**Files:**
- Modify: `internal/domain/model.go` (`Revision.Assets map[string]BlobRef \`json:"assets,omitempty"\``), `internal/domain/outbox.go` (`RetainedPendingAssets` переиспользуется), места создания ревизий (`domain/articles.go PutArticle`, импорт `importer/apply.go`, перенос и копия `transfer/*`, синхронизация `syncer/{pull,reconcile}.go`, перевод `translation/*`, разрешение конфликтов), `domain.Page` (+ `Assets map[string]BlobRef \`json:"-"\``), `domain/reading.go`, `internal/render/markdown.go`, `internal/httpapi/media.go`
- Test: `internal/domain/revision_assets_test.go`, `internal/render/markdown_test.go`, `internal/httpapi/media_test.go`

**Interfaces:**
- Produces: `func RevisionAssets(text, articlePath string, source Source) map[string]BlobRef` (обёртка над `RetainedPendingAssets` с `source.Assets`). Каждая новая ревизия и копия `Published` получают `Assets`. `Page.Assets` заполняется из отображаемой ревизии (или `Published`). Рендер: если у страницы есть ссылка для пути картинки — `"/media/" + sourceID + "/" + url.PathEscape(target) + "?v=" + sha`, иначе прежний URL.
- `media`: без `v` — прежнее поведение. С `v` (64 hex): отдаётся блоб `v`, если в источнике `parts[0]` есть статья, видимая на этом слушателе, у отображаемой версии которой (публичный слушатель — только `Published` публично читаемого источника; частный — `Published` или любая хранимая ревизия) `Assets[parts[1]].SHA == v`; иначе 404. Проверка SHA содержимого и лимит размера — как сейчас.

- [ ] **Step 1: Тесты:** `RevisionAssets` берёт только картинки, на которые ссылается текст; ревизия, записанная через `PutArticle`, несёт `Assets`; после смены картинки в источнике старая ревизия рендерится с `?v=<старый sha>`; `media` с `v` старой опубликованной ревизии отдаёт старый блоб; публичный слушатель с `v` неопубликованной ревизии или приватного источника — 404; `v` не hex — 404.
- [ ] **Step 2:** Run — FAIL → реализовать → PASS. `make test`, скриншоты не нужны (пиксели не меняются).
- [ ] **Step 3:** Commit: `"Bind images to the revision that references them"`.

### Task 4: Чистка при фиксации и формат v21

**Files:**
- Modify: `internal/store/tx.go` (`update`), `internal/store/store.go` (создание пустой базы в v21), `internal/store/validate.go` (верхняя граница 21), `internal/httpapi/articles.go` (`?history=1` — без изменения кода, только тест)
- Test: `internal/store/tx_test.go`, `internal/httpapi/*_test.go`

**Interfaces:**
- Consumes: Task 2.
- Produces: при `db.Version >= 21` `update` перед фиксацией вызывает `PruneArticle(tx.db, key)` в режиме `OnlyArticle(key)` и `PruneAll` в полном режиме; в режиме `OnlyConnections` чистки нет. Базы ниже v21 этим шагом не чистятся (их переносит Task 7).

- [ ] **Step 1: Тесты:** новая база — v21; пять записей статьи → в памяти и после переоткрытия 3 ревизии с номерами 3,4,5; `OnlyArticle` не меняет другие статьи; задание очереди завершено полной фиксацией → защищённая ревизия удалена; владелец `GET ...?history=1` получает 3,4,5; `PUT expected_revision 5` проходит.
- [ ] **Step 2:** Run — FAIL → реализовать → PASS. Commit: `"Prune revision history on every commit"`.

### Task 5: Ревизии отдельными строками SQLite

**Files:**
- Create: `internal/store/sqlite_revisions.go`, `internal/store/sqlite_revisions_test.go`
- Modify: `internal/store/sqlite.go` (`initSQLite`: таблицы; `loadSQLite`: сборка), `internal/store/sqlite_records.go` (`commitSQLite`, `commitSQLiteArticle`), `internal/store/sqlite_benchmark_test.go`

**Interfaces:**
- Таблицы (создаются в `initSQLite` и переносом Task 7):
  `revisions(source_id TEXT NOT NULL, path TEXT NOT NULL, number INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY(source_id,path,number)) WITHOUT ROWID` — `data` = JSON `Revision`;
  `revision_assets(sha256 TEXT NOT NULL, source_id TEXT NOT NULL, path TEXT NOT NULL, number INTEGER NOT NULL, PRIMARY KEY(sha256,source_id,path,number)) WITHOUT ROWID`.
- Запись `article` в `records` хранит `Versions` без поля `text` (номер, SHA, время, картинки); тексты — в `revisions`. Загрузка соединяет их и отказывает (`invalid SQLite record`), если у шапки нет строки ревизии или наоборот, или SHA текста не совпадает.
- Фиксация пишет только разницу: новые ревизии — вставка, удалённые чисткой — удаление (и их `revision_assets`), неизменные не трогаются; удаление статьи удаляет её строки.

- [ ] **Step 1: Тесты:** круговой тест записи/загрузки с ревизиями 7,8,9 и картинками; после пятой правки в таблице `revisions` у статьи ровно 3 строки, строки неизменных ревизий не переписаны (счётчик `changes()` или сравнение `rowid`-независимым способом — решение исполнителя); повреждённая строка ревизии → отказ загрузки и `recoverSQLite` уводит в `Invalid`; бенчмарк: правка одной статьи из 500 с историей пишет O(1) строк ревизий.
- [ ] **Step 2:** Run — FAIL → реализовать → PASS. Commit: `"Store each revision as its own SQLite row"`.

### Task 6: Сборка неиспользуемых блобов

**Files:**
- Create: `internal/store/blobgc.go`, `internal/store/blobgc_test.go`
- Modify: `internal/store/blobs.go` (`writeBlob`: при существующем валидном блобе — `os.Chtimes` на текущее время), `internal/store/tx.go` или `store.go` (запуск), `internal/domain` (функция сбора ссылок)

**Interfaces:**
- Produces: `func ReferencedBlobs(db Database) map[string]bool` (домен: `Source.Assets`, `Revision.Assets` всех ревизий, `Published.Assets`, `SyncJob.Assets`, `Move.Assets`, картинки метаданных источника, любые другие поля `BlobRef`; исполнитель сверяет по `grep BlobRef internal/domain`); `func (s *Store) CollectBlobs(now time.Time) (removed int, err error)` — под `s.mu.Lock()` строит множество, удаляет файлы `blobs/<sha>`, которых нет в нём и у которых `mtime` старше 24 ч; временные `.blob-*` старше 24 ч тоже удаляются. Запуск: после фиксации, уменьшившей множество ссылок (через `AfterCommit`-подобный хук внутри store), и при `Open`; не чаще раза в 5 минут (отметка времени в `Store`).

- [ ] **Step 1: Тесты:** блоб без ссылок старше суток удаляется, моложе — нет; блоб со ссылкой из любой категории не удаляется (по тесту на категорию); гонка: старый неиспользуемый блоб → `WriteBlobs` того же содержимого → `CollectBlobs` → фиксация ссылки → блоб на месте; ограничение частоты; read-only/`Invalid` store не собирает.
- [ ] **Step 2:** Run — FAIL → реализовать → PASS. Commit: `"Remove image blobs that nothing references"`.

### Task 7: Команда `veda migrate-revisions` и отказ открывать старый формат

**Files:**
- Create: `internal/store/migrate_revisions.go`, `internal/store/migrate_revisions_test.go`, `internal/app/commands/migrate_revisions.go`
- Modify: `internal/store/store.go` (`Open`: база ниже v21 → ошибка из Global Constraints), `internal/app` (регистрация подкоманды по образцу `migrate-sqlite`), `internal/store/compat_test.go`, `internal/store/testdata/` (фикстура SQLite v20 с историей >3 ревизий и картинками, синтетическая)

**Interfaces:**
- Produces: `func MigrateRevisions(dir string) (MigrationReport, error)`; `type MigrationReport struct { Articles, KeptRevisions, RemovedRevisions, RemovedBlobs int }`. CLI: `veda migrate-revisions --data DIR` печатает четыре числа.
- Шаги: монопольная блокировка (как `MigrateSQLite`); отказ при работающем писателе, существующем `state.before-revisions.*`, невалидной базе, базе уже v21; копия `state.before-revisions.sqlite` (+ маркер) или `state.before-revisions.json` с fsync; заполнение `Revision.Assets` сохранившихся ревизий и `Published` через `RevisionAssets` по текущему `Source.Assets`; `PruneAll`; v21; для SQLite — новые таблицы и перенос текстов в одной транзакции; проверка после переоткрытия (`integrity_check`, `Validate`, SHA каждой ревизии, наличие каждого упомянутого блоба); fsync каталога; `CollectBlobs`.

- [ ] **Step 1: Тесты:** фикстура v20 SQLite и `testdata/fc35bea` (JSON и SQLite) переносятся, отчёт с ожидаемыми числами, тексты 3 последних ревизий байт в байт; повторный запуск — отказ, копия на месте; запуск при открытом `Store` — отказ; `Open` базы v20 — ошибка с текстом про `veda migrate-revisions`; база после переноса открывается; вывод CLI не содержит путей и текстов фикстуры.
- [ ] **Step 2:** Run — FAIL → реализовать → PASS. `scripts/smoke_store_compat.py` сейчас проверяет, что бинарник `fc35bea` читает базу, записанную текущим кодом; после v21 это неверно по замыслу. Переписать: копия фикстуры → `veda migrate-revisions` текущего кода → `TestWriteForPreviousBinary` пишет ревизию → бинарник `fc35bea` отказывается запускаться (процесс завершается с ошибкой, `/health` не отвечает) → текущий бинарник запускается и отдаёт новую ревизию; отдельно — `fc35bea` по-прежнему запускается на непереработанной копии. Commit: `"Add veda migrate-revisions and refuse pre-v21 stores"`.

### Task 8: Замер, документация

**Files:**
- Create: `scripts/bench_migrate_revisions.py` (синтетическая база: 550 статей, по 10 ревизий, 150 картинок; замер времени `migrate-revisions`, размеров до/после, времени запуска сервиса)
- Modify: `docs/storage.md` (раскладка, чистка, сборка блобов, перенос и откат через PBS), `docs/architecture.md` (владение: правила в `domain`, применение в `store`), `docs/api.md` (`?history=1` возвращает только хранимые ревизии), `docs/roadmap.md` (строка SQLite: индексы и чистка реализованы, развёртывание не выполнено), `README.md` при необходимости

- [ ] **Step 1:** Запустить замер, записать числа в `docs/storage.md` с пометкой «синтетика, этот Mac».
- [ ] **Step 2:** `make lint`, `make test`, `python3 scripts/smoke_store_compat.py`, `python3 scripts/smoke_installed_mcp.py` (официальный Python SDK), `scripts/smoke_ui.py` — всё PASS.
- [ ] **Step 3:** Commit: `"Document revision storage, retention and migration"`.
