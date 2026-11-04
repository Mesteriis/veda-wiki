# Перестройка VEDA, фаза 1: пакеты Go — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Разнести бэкенд из одного пакета `main` в `server/` по пакетам `cmd/veda` и `internal/*` с владением данными, без изменения поведения.

**Architecture:** Перенос снизу вверх. Сначала весь пакет `main` переезжает в `cmd/veda` вместе с `go.mod` в корне. Затем из него по одной вынимаются области в `internal/*`. Чтобы каждый коммит собирался, в `cmd/veda/compat.go` временно живут псевдонимы типов (`type Article = domain.Article`) и переходники функций; последняя задача удаляет файл целиком. Хранилище получает транзакции `View`/`Update`, области работают через них.

**Tech Stack:** Go 1.26, `modernc.org/sqlite`, `goldmark`, `staticcheck` (закреплённая версия, выбирается в Task 2), Python 3 для скриптов проверок.

**Spec:** `docs/superpowers/specs/2026-11-03-restructure-design.md`

## Global Constraints

- Наблюдаемое поведение не меняется: маршруты, JSON, коды ответов, флаги и подкоманды CLI, формат `state.json` и записей SQLite, права файлов, тексты.
- Новый бинарник читает текущую базу LXC161; предыдущий бинарник читает базу после нового.
- Каждый коммит: `go build ./... && go test -race ./... && go vet ./...` зелёные; в конце задачи ещё `make test`.
- Тесты не удаляются и не ослабляются; меняется только способ доступа к данным.
- Не больше 500 строк в файле `.go` (включается в Task 15); в конце нет пакетов `legacy`, `common`, `util`, нет `compat.go`.
- Модуль: `github.com/Mesteriis/veda-wiki`, `go.mod` в корне.
- Правила `AGENTS.md`: loopback по умолчанию, проверка Host, публичный слушатель игнорирует токены, запись из браузера запрещена, секреты не попадают в ответы и логи.

## Review Focus

1. Ограниченный читатель (scoped key, пользователь команды) после переноса видит только свои источники — тест в Task 13 гоняет матрицу ролей и scoped-снимок.
2. Ошибка внутри `store.Update` не меняет ни память, ни диск — тест в Task 5.
3. Чтение базы, записанной старым бинарником `fc35bea`, и обратное чтение — тест в Task 5 на фикстуре SQLite.
4. Публичный слушатель игнорирует токен владельца и cookie после переноса обработчиков — существующий `TestPublic*` и `scripts/smoke_dual_listener.py` в Task 13.
5. Изменение коллекции не её владельцем — скрипт владения в Task 15 падает на синтетическом нарушении.

---

## Правила переноса для всех задач

- Метод `func (s *Store) fooBar(...)` области X становится экспортированной функцией или методом сервиса пакета X с теми же параметрами: `x.FooBar(st *store.Store, ...)` либо `(*x.Service).FooBar(...)`. Выбор формы — исполнителю, но внутри пакета единообразно.
- Код, читавший `s.db` под `s.mu.RLock()`, переходит на `st.View(func(db *domain.Database) error {...})`. Код с `s.mu.Lock(); db := s.clone(); ...; s.commit(db)` переходит на `st.Update(func(tx *store.Tx) error {...})`.
- Тесты переезжают вместе с кодом в тот же пакет. Прямые обращения тестов к `s.db` заменяются на `storetest.Mutate(t, st, func(db *domain.Database){...})` и `storetest.Read(t, st)` (Task 5).
- После переноса в `cmd/veda/compat.go` добавляются псевдонимы и переходники, нужные оставшемуся коду. Каждая задача удаляет из `compat.go` то, что больше не нужно.
- Проверка шага: `go build ./... && go vet ./... && go test -race ./...` из корня.

---

### Task 1: Модуль в корне и пакет `main` в `cmd/veda`

**Files:**
- Move: `server/go.mod`, `server/go.sum` → `go.mod`, `go.sum`; module path → `github.com/Mesteriis/veda-wiki`
- Move: `server/*.go` → `cmd/veda/*.go` (`git mv`, содержимое не меняется)
- Move: embed `server/ui` → `cmd/veda/ui` (gitignored, как сейчас `server/ui`)
- Modify: `Makefile`, `.gitignore`, `.github/workflows/ci.yml`, `scripts/package_release.py`, `scripts/*.py`/`*.sh` с `server/`, `README.md`, `AGENTS.md`, `docs/*.md` (пути)

**Interfaces:**
- Produces: `make build` кладёт бинарник в `./veda`; `make frontend` копирует `web/dist` в `cmd/veda/ui`; тесты Go запускаются из корня `go test ./...`.

- [ ] **Step 1:** `git mv` файлов, правка `module` в `go.mod`, `Makefile`: `cp -R web/dist cmd/veda/ui`, `go build -trimpath -o veda ./cmd/veda`, `go test -race ./... && go vet ./...`.
- [ ] **Step 2:** Найти все `server/` в скриптах, CI и документации (`rg -n "server/|cd server" -g '!web/node_modules'`), заменить на новые пути. В `scripts/package_release.py` путь сборки `./cmd/veda`; `scripts/test_package_release.py` должен пройти.
- [ ] **Step 3:** Run: `make test && python3 -m unittest discover -s scripts -p 'test_*.py'` — Expected: всё зелёное, число Go-тестов как до переноса (`go test ./... -json | grep -c '"Action":"pass","Test"'` сравнить с базовым числом, записанным до Step 1).
- [ ] **Step 4:** Commit: `git commit -m "Move the Go module to the repository root and the program to cmd/veda"`.

### Task 2: Защита размера файлов и `staticcheck`

**Files:**
- Create: `scripts/check_file_size.py`, `scripts/test_check_file_size.py`
- Modify: `Makefile` (цель `lint`, вызывается из `test`)

**Interfaces:**
- Produces: `python3 scripts/check_file_size.py --max 500 --include 'scripts/**/*.py' [--include ...]` — печатает `path: N lines` для нарушений и выходит с кодом 1. Исключения (`*.lock`, `package-lock.json`, `go.sum`, `web/dist/**`, `**/node_modules/**`) — список в скрипте.

- [ ] **Step 1: Тест** `test_reports_files_over_limit`: временный каталог с файлом на 501 строку и на 500; ожидание — код 1 и в выводе только первый файл. `test_excluded_lock_file_is_ignored`: `package-lock.json` на 900 строк не нарушение.
- [ ] **Step 2:** Run `python3 -m unittest scripts/test_check_file_size.py` — FAIL (нет модуля).
- [ ] **Step 3:** Реализовать скрипт (`pathlib.Path.glob`, без зависимостей).
- [ ] **Step 4:** В `Makefile` цель `lint`: `check_file_size.py --max 500 --include 'scripts/**/*.py'` и `go run honnef.co/go/tools/cmd/staticcheck@<версия> ./...`, где `<версия>` — последний выпуск, поддерживающий Go 1.26 (проверить `go run honnef.co/go/tools/cmd/staticcheck@latest -version`), записанный в `Makefile` явно. Если `scripts/smoke.py` (447) проходит, а `staticcheck` ругается на существующий код — исправить замечания отдельным коммитом в этой же задаче, не подавляя их.
- [ ] **Step 5:** Run `make test` — PASS. Commit: `"Check file sizes and run staticcheck in make test"`.

### Task 3: `internal/domain` — модель, ошибки, проверки, чистые правила

**Files:**
- Create: `internal/domain/model.go` (типы из `store.go`, `github.go:26-60`, `move.go`, `people.go:17-40`, `access_keys.go:18-30`, `translation.go`, `source_mirror.go`, `sync_store.go`, `import.go:30-60`), `internal/domain/errors.go`, `internal/domain/validate.go`, `internal/domain/visibility.go`, `internal/domain/outbox.go`, `internal/domain/moves.go`, `internal/domain/display.go`, тесты рядом
- Create: `cmd/veda/compat.go`

**Interfaces:**
- Produces (экспортированные имена, остальное — те же поля и JSON-теги):
  - типы `Source, SourceView, Revision, Article, Database, Translation, CopyOrigin, ReconcileBase, ImportConflict, BlobRef, WikiMetadata, SyncJob, Move, AccessKey, WikiUser, WikiTeam, GitHubAccount, GitHubConnection, SourceMirror`
  - `var ErrInvalid, ErrConflict, ErrNotFound error`
  - `ValidID(string) bool` (бывш. `identifier.MatchString`), `SafeArticlePath(string) bool` (`safePath`), `SafeRelative(string) bool`, `ArticlePath(p, kind string) bool`, `ValidCommitID(string) bool` (`commitID`), `CredentialLike(string) bool` (`secretContent`), `Digest([]byte) string`, `HexKey(string) ([32]byte, error)`, `Title(text, path string) string`
  - `SourcePublic(db Database, src Source, now time.Time) bool`, `SourcePublicReadable(db Database, src Source, now time.Time) bool`, `SourceSyncIssue(db Database, src Source, now time.Time) string`
  - `EnqueueWrite(db *Database, a Article, rev Revision)`, `ReconcileMoves(db *Database)`, `MoveDeletionLocks(db Database, sourceID, path string) bool`, `MoveDeletionLocksConnection(db Database, id string) bool`, `BundleLocks`, `MoveOperationLocks`, `ConflictResolutionBlocked(db Database, sourceID, path string) bool`, `DisplayedRevision(a Article, rev Revision, public bool) (Revision, bool)`, `TranslationFingerprint(...)` — сигнатуры как у нынешних функций.
- `compat.go`: `type Article = domain.Article` и т. д. для всех типов; `var identifier = domain.IDPattern` или переходник `func identifierMatch`; `ErrInvalid = domain.ErrInvalid`…; переходники `sourcePublic = domain.SourcePublic` и т. д.

- [ ] **Step 1:** Перенести типы и функции без изменения логики, экспортировать имена. Чистые функции не трогают часы — `now` уже параметр; где функция вызывала `time.Now()` внутри (`ReconcileMoves`?), передать `now` параметром и обновить вызовы.
- [ ] **Step 2:** Перенести в `internal/domain` тесты, проверяющие только эти функции (`title_test.go`, проверки путей из `main_test.go`, `translation_fingerprint_test.go`); остальные тесты остаются в `cmd/veda` и работают через `compat.go`.
- [ ] **Step 3:** Run `go list -f '{{join .Imports "\n"}}' ./internal/domain` — Expected: нет `os`, `net`, `net/http`, `database/sql`, `os/exec` (постоянная проверка появится в Task 15).
- [ ] **Step 4:** Run проверку шага — PASS. Commit: `"Extract the domain model and pure state rules"`.

### Task 4: `internal/render`, `internal/search`, `internal/github`

**Files:**
- Create: `internal/render/markdown.go` (`render`, `unicodeIDs`, `headingID`, `sourceTarget`, `repositoryTarget`, `directoryReadmeTarget` из `main.go`), `internal/render/graph.go` (`graphEdges`), тесты `graph_test.go` и render-тесты из `main_test.go`
- Create: `internal/search/search.go` (`searchTerms`, `searchRevisions*`, `searchScore*`; `Find` вместо `Store.findArticles`), тесты `search_test.go`, бенчмарки
- Create: `internal/github/client.go` (весь `github.go` кроме типов, ушедших в `domain`), `internal/github/errors.go` (`RemoteError`, `ErrorCode`, `RetryAt`), `internal/github/githubtest/fake.go` (`fakeGitHub` и его помощники из `sync_test.go`), тесты `github_rate_limit_test.go`, `github_repository_name_test.go`

**Interfaces:**
- Produces:
  - `render.Page(p domain.Page) (html, toc template.HTML, err error)` — `Page` (структура страницы из `main.go:31`) переезжает в `domain` как `domain.Page`; `render.GraphEdges(pages []domain.Page) []render.Edge` (бывш. `GraphEdge`), `render.SourceTarget(pagePath, dest string) (sourceID, target string, ok bool)`, `render.DirectoryReadmeTarget`
  - `search.Terms(q string) [][]string`, `search.Revisions(query, path, sourceTitle, original, displayed string, terms [][]string) int`, `search.Find(db domain.Database, query, sourceID string, public bool, now time.Time) []search.Hit` (`Hit{SourceID, Path, Title}` с прежними JSON-тегами)
  - `github.Client` (бывш. `GitHubClient`) с методами `VerifyAccount, Token, Repository, Head, Tree, Blob, UpdateBranch, Push, PushFiles, Remove`; `github.New() *Client`, `github.NewForTest(base string, hc *http.Client) *Client`; `github.RemoteError{Code string}`, `github.ErrorCode(error) string`, `github.RetryAt(error) time.Time`, `github.RepositoryName(string) (string, error)`, `github.ValidBranch(string) bool`; типы `Tree`, `TreeEntry`, `Commit`, `RepositoryInfo`
  - `githubtest.Fake` с прежними методами `modify`, `advance`, `writeCount` → `Modify`, `Advance`, `WriteCount`; `githubtest.New(t, files map[string]string) *Fake`, `(*Fake).Client() *github.Client`
- Consumes: `domain` из Task 3.

- [ ] **Step 1:** Перенести `render` и его тесты; `Store.findArticles` переписать как `search.Find` над снимком, полученным через `View` в вызывающем коде (пока — через `compat.go`).
- [ ] **Step 2:** Перенести `github` и `githubtest`; в `cmd/veda` тесты, использовавшие `newFakeGitHub`, вызывают `githubtest.New`.
- [ ] **Step 3:** Run проверку шага, бенчмарки поиска: `go test -run '^$' -bench 'FindArticles' ./internal/search` — работают. Commit: `"Extract rendering, search and the GitHub client"`.

### Task 5: `internal/store` — транзакции и бэкенды

**Files:**
- Create: `internal/store/store.go` (`Open`, `privateDataDir`, `validate`, `clone`), `internal/store/tx.go`, `internal/store/json.go` (запись `state.json`), `internal/store/sqlite.go`, `internal/store/sqlite_records.go`, `internal/store/migrate.go` (`MigrateSQLite`), `internal/store/blobs.go` (`ReadBlob`, `WriteBlob`, `VerifyBlob` — бывш. `readAsset`/`verifyAsset`/запись блобов из `import.go`), `internal/store/scoped.go` (`scopedViewLocked`)
- Create: `internal/store/storetest/storetest.go`
- Test: `internal/store/*_test.go` (из `sqlite_store_test.go`, `v7…v13_fixture_test.go`, `shutdown`-независимые части `store`-тестов), `internal/store/tx_test.go`
- Test fixture: `internal/store/testdata/fc35bea.sqlite` + `state.json` (создать бинарником `fc35bea` из синтетических данных, см. Step 1)

**Interfaces:**
- Produces:
  - `store.Open(dir string) (*Store, error)`, `(*Store).Dir() string`, `(*Store).Invalid() bool`, `(*Store).IsSQLite() bool`
  - `(*Store).View(fn func(db *domain.Database) error) error` — под `RLock`, `db` только для чтения
  - `(*Store).Update(fn func(tx *Tx) error) error` — под `Lock`, `tx.DB()` возвращает клон; при `nil` от `fn` фиксирует по области `tx`, иначе ничего не меняет
  - `(*Tx).DB() *domain.Database`, `(*Tx).OnlyArticle(key string)` (бывш. `commitWithArticle`), `(*Tx).OnlyConnections()` (бывш. `commitConnections`)
  - `(*Store).Scoped(sources []string) (*Store, error)` — неизменяемый снимок для ограниченного читателя; `Update` на нём возвращает ошибку
  - `(*Store).ReadBlob(sha string) ([]byte, error)`, `(*Store).WriteBlob(data []byte) (domain.BlobRef, error)`, `(*Store).VerifyBlob(ref domain.BlobRef) error`
  - `store.MigrateSQLite(dir string) error`
  - `storetest.New(t) *store.Store`, `storetest.Source(t, st, id, visibility string)`, `storetest.Mutate(t, st, fn func(*domain.Database))` (меняет и фиксирует), `storetest.Read(t, st) domain.Database` (копия)
- Consumes: `domain`.

- [ ] **Step 1: Фикстура совместимости.** Собрать `fc35bea` (`git worktree add /tmp/veda-fc35bea fc35bea && make -C /tmp/veda-fc35bea build`), запустить на временном каталоге, создать источник, две ревизии статьи, ключ доступа, пользователя через REST/CLI, выполнить `veda migrate-sqlite`, остановить; скопировать `state.json` и `state.sqlite` в `internal/store/testdata/fc35bea/`. Записать команды в комментарий теста.
- [ ] **Step 2: Тесты.**
  - `TestOpenFc35beaSQLite`: `Open` на копии фикстуры; ожидания — статья с двумя ревизиями, ключ и пользователь на месте.
  - `TestUpdateErrorLeavesMemoryAndDiskUnchanged`: `Update` меняет статью и возвращает ошибку; затем `View` видит прежний текст, и `Open` того же каталога видит прежний текст (JSON и SQLite, табличный тест).
  - `TestOnlyConnectionsWritesNoArticleRecords`: перенос `TestSQLiteConnectionStatusWritesOnlyConnections` на `Tx.OnlyConnections`.
  - `TestScopedStoreRejectsUpdate`: `Scoped([]string{"a"})` не видит источник `b`, `Update` возвращает ошибку.
- [ ] **Step 3:** Run `go test ./internal/store/...` — FAIL (пакета нет).
- [ ] **Step 4:** Перенести код хранилища, реализовать `View`/`Update`/`Tx` поверх нынешних `clone`/`commitTargeted`. `cmd/veda/compat.go`: `Store` — обёртка с полем `st *store.Store`, оставшиеся методы области временно вызывают `st.View`/`st.Update`.
- [ ] **Step 5: Обратная совместимость.** Скопировать каталог, открыть новым кодом, сделать `put` через `Update`, затем открыть каталог бинарником `fc35bea` (`/tmp/veda-fc35bea/veda --data <dir> --listen 127.0.0.1:0 --public-listen 127.0.0.1:0` + `/health`) — Expected 200 и статья читается через `/api/v1/articles` с ключом владельца. Оформить как скрипт `scripts/smoke_store_compat.py`.
- [ ] **Step 6:** Run проверку шага и `python3 scripts/smoke_store_compat.py`. Commit: `"Move storage behind View and Update transactions"`.

### Task 6: `internal/auth` — ключи, люди, сессии

**Files:**
- Create: `internal/auth/keys.go` (`access_keys.go` без `scopedView*`, который ушёл в `store`), `internal/auth/people.go`, `internal/auth/passwords.go` (`passwordHash`, Argon2, мьютекс паролей), `internal/auth/sessions.go` (`readerSessions`, cookie-помощники), `internal/auth/limiter.go` (`allowAttempt`), тесты `access_keys_test.go`, `people_test.go`, `admin_role_test.go`, `session_test.go`, `scoped_session_test.go`

**Interfaces:**
- Produces:
  - `auth.Service{Store *store.Store}` с методами `ListAccessKeys`, `CreateAccessKey`, `UpdateAccessKey`, `RevokeAccessKey`, `AccessScope(token string, now time.Time) []string`, `ValidScopeHash`, `ScopedView(token string, now time.Time) (*store.Store, bool, error)`, `ScopedViewHash`, `ScopedViewUser`, `SaveUser`, `SaveTeam`, `SetUserAdmin`, `CreateSuperuser`, `BootstrapAdmin`, `FinishAdminSetup`, `AuthenticateUser`, `AdminSessionValid`, `UserSessionValid`, `UserViews`, `TeamViews` — параметры как у нынешних методов
  - `auth.Sessions` (бывш. `readerSessions`) с `Valid`, `ValidCookie`, `UserCookie`, `NamedUserCookie`, `ScopedCookie`, `AllowAttempt(kind, identity string, now time.Time) bool`, `Issue(...)`, `Revoke(...)`; константы имён cookie
  - `auth.SecureBrowserRequest(*http.Request) bool`, `auth.SameOrigin(*http.Request) bool`
- Consumes: `store`, `domain`.

- [ ] **Step 1:** Перенести код и тесты; коллекции `AccessKeys`, `Users`, `Teams` теперь меняются только здесь.
- [ ] **Step 2:** `sessionAPI` пока остаётся в `cmd/veda` (переедет в `httpapi` в Task 12) и вызывает `auth.Sessions`.
- [ ] **Step 3:** Run проверку шага. Commit: `"Extract access keys, people and sessions into auth"`.

### Task 7: `internal/importer`

**Files:**
- Create: `internal/importer/metadata.go` (`parseMetadata`, `validateMetadata`), `internal/importer/checkout.go` (`prepareCheckout`, `boundedGitOutput`), `internal/importer/apply.go` (`importBatch`, `applyImport`, `storeImportAssets`, `imageRef`), `internal/importer/decode.go`, `internal/importer/compare.go`, `internal/importer/conflicts.go` (`conflicts`, `resolveConflict`, `supersedeImportConflicts`, `hasImportConflict`), `internal/importer/local_remove.go`, `internal/importer/observations.go` (`observed`, `withheld` — состояние в памяти, бывш. поля `Store`), тесты `import_*_test.go`, `local_import_test.go`, `local_remove_test.go`, `observation_test.go`, `large_article_test.go`, `public_partial_sync_test.go` (если не синк-сценарий)

**Interfaces:**
- Produces: `importer.Batch`, `importer.Result`, `importer.RemoteProof` (бывш. `ImportBatch`, `ImportResult`, `remoteProof`); `importer.Service{Store, Observations *Observations}` с `Apply(batch Batch, proof *RemoteProof, localRoot bool) (Result, error)`, `PrepareCheckout(...)`, `Conflicts(...)`, `ResolveConflict(...)`, `RemoveLocalImportedSource(...)`, `MarkObserved(sourceID string)`, `MarkWithheld(sourceID string, n int)`; `importer.Observations` с `Observed(sourceID) (time.Time, bool)`, `Withheld(sourceID) (int, bool)`; `importer.Decode(rw, r, *Batch) error`; `importer.ComparePreview(...)`.
- Consumes: `store`, `domain`, `auth` (нет).

- [ ] **Step 1:** Перенести код и тесты. `SourceView` (поля наблюдения) собирается в вызывающем коде из `domain.Source` и `importer.Observations`.
- [ ] **Step 2:** Run проверку шага. Commit: `"Extract repository import and conflicts"`.

### Task 8: `internal/transfer`

**Files:**
- Create: `internal/transfer/copy.go`, `internal/transfer/linked.go` (`copy_linked.go`), `internal/transfer/move.go` (`moveArticle`, `cancelMove`, `moves`, `transferAssets`, `linkContents`), `internal/transfer/assets.go` (`assetsPresent`, `linksPresent`, `remoteAssetPath`, `retainedPending*`, `verifyMoveAssets`), `internal/transfer/plan.go` (`transfer_plan.go`), тесты `copy*_test.go`, `move*_test.go`, `transfer_plan_test.go`, `partial_transfer_test.go`, `move_translation_test.go`

**Interfaces:**
- Produces: `transfer.Service{Store}` с `PrepareCopy`, `Copy` (бывш. `copyArticle`), `Move`, `CancelMove`, `Moves`, `PlanTransfer`, `DirectoryTransferSource`; типы `CopyRequest`, `MoveRequest`, `Plan` (бывш. `copyRequest` и т. п., JSON-теги прежние).
- Consumes: `store`, `domain` (правила `ReconcileMoves`, `MoveDeletionLocks`, `EnqueueWrite`).

- [ ] **Step 1:** Перенести; коллекция `Moves` меняется только здесь и в правилах `domain`.
- [ ] **Step 2:** Run проверку шага. Commit: `"Extract copies, moves and transfer plans"`.

### Task 9: `internal/translation`

**Files:**
- Create: `internal/translation/validate.go` (`validateTranslation`, `protectedMarkdown`, `validTranslationFingerprint`), `internal/translation/submit.go` (`submitTranslation`, `saveTranslation`, `translationCandidates`), `internal/translation/worker.go`, `internal/translation/chunks.go` (`translationChunks`, `containsRussianProse`), тесты `translation*_test.go`, `multisource_translation_test.go`

**Interfaces:**
- Produces: `translation.Service{Store}` с `Validate`, `Submit`, `Candidates`; `translation.Request` (бывш. `translationRequest`); `translation.NewWorker(st *store.Store, url, model, protocol string) (*Worker, error)`, `(*Worker).TokenFile string`, `(*Worker).Run(ctx, interval)`.
- [ ] **Step 1:** Перенести. Если `translation_worker_test.go` (753) после переноса больше 500 строк — разделить по протоколам (`worker_ollama_test.go`, `worker_openai_test.go`, `worker_mlx_test.go`) уже здесь.
- [ ] **Step 2:** Run проверку шага. Commit: `"Extract translation validation and the local worker"`.

### Task 10: `internal/syncer`

**Files:**
- Create: `internal/syncer/manager.go` (`SyncManager`: `New`, `Wake`, `Run`, `Once`), `internal/syncer/connect.go` (`configure`, `connect`), `internal/syncer/pull.go` (`syncSource`, `remoteBatch`, `cachedBlob`, `gitBlobDigest`, `importablePath`), `internal/syncer/push.go` (`syncWrites`, `pushPending`, `pendingBundle`, `bundleHasCredentialLikeText`), `internal/syncer/moves.go` (`deletePendingMove`, `deleteMove`), `internal/syncer/state.go` (`sync_store.go` без перенесённого в `domain`: `connectionStatusFor`, `setJobFor`, `prepareJob`, `publishJob`, `cancelBundle`, `observeGitHubHead`, `observeChangedHead`, `retryConnection`, `pauseConnection`, `recordSkippedRepository`, `sourceSyncState`), `internal/syncer/accounts.go` (`saveAccount`, `accounts`, `secret`, `atomicPrivateFile`, `randomID`), `internal/syncer/mirror.go`, `internal/syncer/reconcile.go`, `internal/syncer/adoption.go`
- Test: `sync_test.go` (1107 строк) разделить на `pull_test.go`, `push_test.go`, `publication_test.go`, `visibility_test.go`; плюс `github_*_test.go`, `source_mirror*_test.go`, `source_reconcile_test.go`, `adoption_test.go`, `sync_process_test.go`

**Interfaces:**
- Produces: `syncer.New(st *store.Store, gh *github.Client, imp *importer.Service) *Manager`; методы `Wake`, `Run(ctx, interval)`, `Once(ctx)`, `Configure`, `Connect`, `PublishJob`, `CancelBundle`, `RetryConnection`, `PauseConnection`, `Accounts`, `SaveAccount`, `Connections`, `Jobs`, `Mirrors`, `HasEnabledMirror`, `ConfigureMirror`, `ReconcileSources`; `(*Manager).LocalImportSource string`; типы запросов `ConnectRequest`, `ConfigureRequest`, `MirrorRequest`, `ReconcileRequest`.
- Consumes: `github`, `importer` (`Apply`, `Observations`), `store`, `domain`.

- [ ] **Step 1:** Перенести; коллекции `Outbox`, `Connections`, `Accounts`, `SourceMirrors` меняются только здесь. Статусные записи используют `tx.OnlyConnections()`.
- [ ] **Step 2:** Разделить `sync_test.go` по сценариям; число тестов не меняется (сверить `go test -list . ./internal/syncer | wc -l` с числом перенесённых).
- [ ] **Step 3:** Run проверку шага и `VEDA_MIRROR_SCALE=realistic go test -run '^TestMirrorSyntheticScale$' ./internal/syncer -timeout 10m`. Commit: `"Extract repository synchronization"`.

### Task 11: `internal/agentctx`

**Files:**
- Create: `internal/agentctx/context.go` (`agentContext`, `sourceStatusLocked`), `internal/agentctx/proposal.go` (`proposalBase`, `newProposalBase`), тесты `context_test.go`, `proposal_test.go`, `excerpt_test.go` (если про контекст)
- **Interfaces:** Produces `agentctx.Context(st *store.Store, sourceID, target string, public bool) (Result, error)`, `agentctx.ProposalBase(...)`, `agentctx.NewProposalBase(...)`, `agentctx.SourceStatus(db domain.Database, sourceID string, ...)`.
- [ ] **Step 1:** Перенести. Run проверку шага. Commit: `"Extract agent context and proposals"`.

### Task 12: `internal/httpapi`

**Files:**
- Create: `internal/httpapi/server.go` (бывш. `Wiki`, `ServeHTTP`: проверка Host, выбор роли, ограниченный снимок), `internal/httpapi/roles.go` (`authorized`, `readerAuthorized`, guard из `api`, `routeAPI`-проверка роли), `internal/httpapi/respond.go` (`jsonResponse`, `decodeBody`, `decodeLimited`, `apiError`), `internal/httpapi/state.go` (`/api/state`, `/health`, `View`/`Card`/`Group`/`SourceWarning`), `internal/httpapi/articles.go`, `internal/httpapi/excerpts.go`, `internal/httpapi/sources.go`, `internal/httpapi/moves.go`, `internal/httpapi/translation.go`, `internal/httpapi/conflicts.go`, `internal/httpapi/accessadmin.go` (access-keys, users, teams, `browserAccessAPI`), `internal/httpapi/github.go` (`githubAPI`, `adminGitHubAPI`), `internal/httpapi/sessions.go` (`sessionAPI`), `internal/httpapi/local.go` (маршруты `/api/local/*` для root на сокете), `internal/httpapi/media.go`, `internal/httpapi/assets.go` (встроенный UI)
- Create: `internal/webui/embed.go` (`//go:embed dist`), `Makefile` копирует `web/dist` → `internal/webui/dist`
- Test: все тесты HTTP-уровня (`main_test.go`, `api`-тесты, `route_authorization_test.go`, `reader_test.go`, `public_*_test.go`, `state_etag_test.go`, `browser_*_test.go`, `multisource_visibility_test.go`, `recent_pages_test.go`, `source_metadata_view_test.go`, `public_article_metadata_test.go`, `media`-тесты)

**Interfaces:**
- Produces: `httpapi.Config{Public, ReaderLocal, ReaderKeyRequired bool; LocalImportSource string; AllowedHosts []string; OwnerToken, ReaderToken string}`; `httpapi.Deps{Store *store.Store; Auth *auth.Service; Sessions, AdminSessions, AccessSessions *auth.Sessions; Sync *syncer.Manager; Import *importer.Service; Transfer *transfer.Service; Translation *translation.Service}`; `httpapi.New(cfg Config, deps Deps) http.Handler`; `httpapi.LocalRootKey` (ключ контекста для сокета).
- Consumes: все области.

- [ ] **Step 1: Тест** `TestEveryRouteKeepsItsRole`: перенос `TestMutatingRoutesRefuseNonOwners` + добавить GET-маршруты админки (`/api/access/*`, `/api/admin/github/*`) — без cookie 404; scoped-ключ на `/api/state` не видит чужой источник (существующие проверки из `scoped_snapshot_isolation_test.go`).
- [ ] **Step 2:** Перенести обработчики по файлам; общий guard ролей остаётся в маршрутизации, как `routeAPI`.
- [ ] **Step 3:** Run проверку шага, затем `make build && python3 scripts/smoke_dual_listener.py && python3 scripts/smoke_superuser.py`. Commit: `"Split HTTP handlers into the httpapi package"`.

### Task 13: `internal/app` и тонкий `cmd/veda`

**Files:**
- Create: `internal/app/run.go` (бывш. `main` после разбора подкоманд: флаги, проверки, блокировка, слушатели, воркеры), `internal/app/listen.go` (`serveUntilStopped`, `listenReaderSocket`, `loadOrCreateKey`), `internal/app/peer_linux.go`, `internal/app/peer_other.go`, `internal/app/commands/{accesskey,github,importcmd,superuser,migrate}.go`
- Modify: `cmd/veda/main.go` — только разбор `os.Args[1]` и вызов `app.Run(args)` или `commands.X(args)`
- Delete: `cmd/veda/compat.go` и все оставшиеся файлы `cmd/veda`, кроме `main.go`
- Test: `main_test.go` (запуск), `shutdown_test.go`, `access_key_command_test.go`, `superuser_command_test.go`, `reader_socket_test.go`, интеграционные сценарии (`multisource_mcp_integration_test.go`, `agent_routing_mcp_test.go`, `translation_import_integration_test.go`) → `internal/app`

**Interfaces:**
- Produces: `app.Run(ctx context.Context, args []string) error`; `commands.AccessKey(args []string, out io.Writer) error`, `commands.GitHub(args) error`, `commands.Import(args) error`, `commands.CreateSuperuser(args) error`, `commands.MigrateSQLite(args) error`.
- [ ] **Step 1:** Перенести; `cmd/veda/main.go` не длиннее 40 строк.
- [ ] **Step 2:** Run полный набор: `make test`, `make build`, все внешние проверки из CI (`scripts/smoke*.py`, `smoke_ui.py` с локальным Chrome, MCP-тесты с `VEDA_TEST_MCP_PYTHON`), `python3 scripts/smoke_store_compat.py`. Expected: всё зелёное.
- [ ] **Step 3:** Commit: `"Assemble the application in internal/app with a thin cmd/veda"`.

### Task 14: Разделить оставшиеся длинные файлы

- [ ] **Step 1:** Run `python3 scripts/check_file_size.py --max 500 --include '**/*.go'` — список нарушителей.
- [ ] **Step 2:** Каждый разделить по ответственности (код — по операциям, тесты — по сценариям), не меняя логику. Число тестов не меняется: `go test -list . ./... | grep -c '^Test'` до и после.
- [ ] **Step 3:** Run проверку шага. Commit: `"Split remaining files over 500 lines"`.

### Task 15: Защита границ, документация, таблица тестов

**Files:**
- Create: `scripts/check_architecture.py`, `scripts/test_check_architecture.py`
- Create: `docs/architecture.md` (пакеты, направление зависимостей, владение коллекциями)
- Create: `docs/superpowers/notes/2026-11-03-test-mapping.md` (старый файл теста → новый пакет/файл)
- Modify: `Makefile` (`lint` включает `--include '**/*.go'` и `check_architecture.py`), `README.md`, `AGENTS.md`, `docs/*.md`, `.github/workflows/ci.yml`

**Interfaces:**
- Produces: `check_architecture.py` проверяет: (1) `internal/domain` не импортирует `os`, `net`, `net/http`, `database/sql`, `os/exec`; (2) области не импортируют друг друга, кроме разрешённых пар `syncer→importer`, `httpapi→*`, `app→*`; (3) присваивания в `.Outbox[`, `.Connections[`, `.Accounts[`, `.SourceMirrors[`, `.Moves[`, `.AccessKeys[`, `.Users[`, `.Teams[` и `delete(...)` по ним встречаются только в пакете-владельце, в `internal/domain` и в `internal/store`; тестовые файлы `_test.go` и `storetest` исключены.

- [ ] **Step 1: Тесты** `test_flags_foreign_collection_write`: временное дерево, где `internal/auth/x.go` пишет `db.Moves["k"] = v` — код 1 и путь в выводе; `test_allows_owner_write`: то же в `internal/transfer/x.go` — код 0; `test_flags_domain_io_import`.
- [ ] **Step 2:** Реализовать (разбор `go list -json ./...` для импортов, регулярные выражения для присваиваний).
- [ ] **Step 3:** Run `make test` — PASS на реальном дереве.
- [ ] **Step 4:** Документация и таблица соответствия тестов. Commit: `"Guard package boundaries and document the architecture"`.
- [ ] **Step 5:** Открыть PR `refactor/structure` → `main` с описанием фаз и ссылками на спецификацию и таблицу тестов.
