# Перестройка VEDA, фаза 2: MCP-адаптер на Go — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Заменить `mcp/server.py` и `mcp/keyed_stdio_launcher.py` подкомандами `veda mcp` и `veda mcp-relay` с тем же поведением для агентов.

**Architecture:** Пакет `internal/mcp` из трёх частей: `client` (REST по loopback/HTTPS или HTTP через Unix-сокет, ключи, лимиты), `tools` (описания и схемы — данные, дословно из Python-версии), `handlers` (по одному на инструмент). Подкоманды регистрируются в `internal/app/commands`. Совместимость доказывают существующие Python smoke-тесты на официальном Python SDK, запущенные против `veda mcp`.

**Tech Stack:** Go 1.26, `github.com/modelcontextprotocol/go-sdk` v1.8.0 (`mcp.NewServer`, `(*Server).AddTool(*Tool, ToolHandler)`, `(*Server).Run(ctx, &mcp.StdioTransport{})`), Python 3.11+ и `mcp==1.30.0` только для проверок.

**Spec:** `docs/superpowers/specs/2026-11-03-restructure-design.md`

## Global Constraints

- Фаза 1 слита: есть `internal/app/commands`, модуль в корне.
- Имена инструментов, описания, `inputSchema` (включая `additionalProperties: false`, `minLength`, `maximum`), инструкции сервера, имя сервера `veda` и версия `0.1.0` — дословно как в `mcp/server.py` на `fc35bea`.
- Набор только для чтения (13 инструментов) при `--reader-socket`, `--reader-key-file`, `--reader-key-preamble` — тот же.
- Проверки клиента прежние: loopback HTTP или явно разрешённый HTTPS только для читателя с файлом ключа; без логина/пароля, пути, запроса и фрагмента в URL; без прокси и перенаправлений; файл ключа — обычный файл владельца без прав группы/других, ровно 64 hex-символа; сокет — абсолютный путь, сокет и каталог принадлежат текущему пользователю, права `0700`/без группы; ответ не больше 24 МБ; таймауты 15 с, 120 с для `sources/reconcile` и `sources/mirrors`, 360 с для подключения GitHub с `adopt_existing: true`.
- Тексты отказов: `VEDA request refused: <причина> (HTTP <код>[, <код ошибки>]).` с той же таблицей причин; прочие ошибки — `VEDA request refused: invalid input, unavailable source, authorization or revision conflict.`
- `veda_search` вызывает `/api/v1/search?q=…&limit=20`.
- Ключ из `--reader-key-preamble` читается ровно 64 байта со stdin до начала MCP-сообщений.

## Review Focus

1. Аргументы с лишними полями или неверными типами — отказ без запроса к серверу (Go SDK не проверяет схему для `AddTool`) — тест в Task 2.
2. Ответ сервера больше 24 МБ — отказ, а не нехватка памяти — тест в Task 1.
3. Файл ключа с правами `0644` или симлинк — отказ до запроса — тест в Task 1.
4. Перенаправление 302 с REST-адреса — не следовать — тест в Task 1.
5. `mcp-relay`: ключ не попадает в argv и окружение дочернего процесса — тест в Task 3.

---

### Task 1: `internal/mcp/client`

**Files:**
- Create: `internal/mcp/client/client.go`, `internal/mcp/client/keys.go`, `internal/mcp/client/socket.go`, `internal/mcp/client/refusal.go`, тесты `client_test.go`, `keys_test.go`, `socket_test.go`, `refusal_test.go`

**Interfaces:**
- Produces:
  - `client.Options{URL, KeyFile, ReaderKeyFile, SocketPath string; KeyValue string; AllowRemoteReaderHTTPS bool}`
  - `client.New(opts Options) (*Client, error)` — все проверки конфигурации из Python `Client.__init__`
  - `(*Client).ReadOnly() bool`
  - `(*Client).Do(ctx context.Context, method, route string, body any) (json.RawMessage, error)`
  - `client.Refused{Status int; Code string}` с `Error()` и `Message() string`; `client.Timeout(method, route string, body any) time.Duration`

- [ ] **Step 1: Тесты** (порт `scripts/test_mcp_timeout.py`, `test_mcp_https.py`, `test_mcp_errors.py` + Review Focus):
  - `TestTimeoutsByRoute`: ожидания `360s, 15s, 15s, 120s, 120s` для тех же пяти вызовов, что в `test_mcp_timeout.py`.
  - `TestRejectsNonLoopbackAndCredentialsInURL`: `http://example.com`, `http://u:p@127.0.0.1`, `http://127.0.0.1/path`, `http://127.0.0.1?x=1` → ошибка `New`.
  - `TestRemoteHTTPSOnlyForReaderKeyFile`: `https://veda.example` с `AllowRemoteReaderHTTPS` и `ReaderKeyFile` — ок; с `KeyFile` — ошибка.
  - `TestKeyFileMustBePrivateRegular`: `0644` → ошибка; симлинк → ошибка; 63 символа → ошибка.
  - `TestDoesNotFollowRedirect`: `httptest.Server` отвечает 302 → `Refused{Status:302}`.
  - `TestResponseLimit`: тело `24<<20 + 1` байт → ошибка «exceeds MCP limit».
  - `TestRefusalMessage`: 409 c `{"error":"revision conflict"}` → `VEDA request refused: revision or state conflict; reread before retrying (HTTP 409, revision conflict).`; 403 с телом `Read-only access` → без текста тела; 502 с `{"error":"/private/path canary"}` → без `canary`.
  - `TestSocketMustBePrivate`: сокет в каталоге `0755` → ошибка.
- [ ] **Step 2:** Run `go test ./internal/mcp/client` — FAIL.
- [ ] **Step 3:** Реализовать: `http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(...) error { return http.ErrUseLastResponse }}`; для сокета — `Transport.DialContext` на `unix`; чтение ключа через `os.OpenFile` с `O_NOFOLLOW` и `Fstat`.
- [ ] **Step 4:** Run — PASS. Commit: `"Add the Go MCP REST client with the adapter's safety checks"`.

### Task 2: `internal/mcp` — инструменты, обработчики, `veda mcp`

**Files:**
- Create: `internal/mcp/tools.go` (таблица `[]ToolSpec{Name, Description string; Schema json.RawMessage; ReadOnly bool}` и `Instructions`), `internal/mcp/server.go` (`NewServer(c *client.Client) *mcp.Server`), `internal/mcp/handlers_read.go`, `internal/mcp/handlers_prepare.go`, `internal/mcp/handlers_write.go`, `internal/mcp/args.go` (строгий разбор аргументов), тесты `tools_test.go`, `handlers_test.go`
- Create: `internal/app/commands/mcp.go` (`MCP(args []string, stdin io.Reader, stdout io.Writer) error`), регистрация в `cmd/veda/main.go`
- Create: `internal/mcp/testdata/tools.json` — выгрузка `list_tools` Python-адаптера `fc35bea` (имя, описание, схема) для owner- и reader-режима

**Interfaces:**
- Consumes: `client.Client` из Task 1.
- Produces: `mcp.NewServer(c *client.Client) *sdkmcp.Server`; подкоманда `veda mcp` с флагами `--url` (по умолчанию `http://127.0.0.1:8080`), `--key-file`, `--reader-key-file`, `--reader-key-preamble`, `--reader-socket`, `--allow-remote-reader-https` и прежними правилами взаимоисключения.

- [ ] **Step 1: Получить эталон.** Скрипт `scripts/dump_mcp_tools.py` (официальный Python SDK) запускает Python-адаптер `fc35bea` в owner- и reader-режиме против временного VEDA и пишет `tools.json`. Закоммитить скрипт и результат.
- [ ] **Step 2: Тесты.**
  - `TestToolsMatchPythonAdapter`: для owner и reader список имён, описаний и схем равен `testdata/tools.json` (сравнение JSON после нормализации порядка ключей).
  - `TestUnknownArgumentRefusedWithoutRequest`: `veda_read` с `{"source_id":"a","path":"b","extra":1}` → `IsError`, у фальшивого сервера 0 запросов.
  - `TestSearchUsesBoundedEndpoint`: `veda_search {"query":"vps lxc"}` → запрос `/api/v1/search?limit=20&q=vps+lxc`, `structuredContent` равен ответу.
  - `TestPrepareUpdateRejectsStaleRevision`: `proposal-base` вернул `revision: 3`, вызов с `expected_revision: 2` → отказ, без записи.
  - `TestPrepareTranslationRequiresProgramDocs`: источник `kind: wiki` → отказ.
  - `TestReaderModeHidesWriteTools`: в reader-режиме `veda_write_article` неизвестен.
- [ ] **Step 3:** Run — FAIL.
- [ ] **Step 4:** Реализовать: инструменты регистрируются циклом по таблице, `AddTool(&sdkmcp.Tool{Name, Description, InputSchema: schema}, handler)`; обработчик сначала строго разбирает аргументы (`json.Decoder.DisallowUnknownFields`, проверки `minLength`/`minimum`/`maximum`/`const` по таблице), затем повторяет логику Python-обработчика; ответ — `TextContent` с JSON (`ensure_ascii=False`-эквивалент: `json.Encoder.SetEscapeHTML(false)`) и `StructuredContent`.
- [ ] **Step 5:** Run Go-тесты — PASS. Затем внешние проверки против `veda mcp`: в `scripts/smoke.py`, `scripts/smoke_mcp_https.py`, `scripts/check_agent_readers.py`, Go-интеграционных тестах с `VEDA_TEST_MCP_PYTHON` заменить запуск `mcp/server.py` на `[ROOT/veda, "mcp", ...]`. Run всех — PASS.
- [ ] **Step 6:** Commit: `"Serve MCP from the veda binary"`.

### Task 3: `veda mcp-relay`

**Files:**
- Create: `internal/app/commands/mcprelay.go`, тест `mcprelay_test.go`

**Interfaces:**
- Produces: `veda mcp-relay --key-file PATH -- COMMAND [ARGS...]` — как `keyed_stdio_launcher.py`: читает ключ (те же проверки файла), запускает дочерний процесс, пишет в его stdin ровно 64 байта ключа, затем пересылает stdin/stdout в обе стороны; код выхода — код дочернего процесса.
- [ ] **Step 1: Тесты** `TestRelaySendsKeyFirstAndForwards`: дочерний процесс — тестовый бинарник, который читает 64 байта и эхо-ответ; ожидание — первые 64 байта равны ключу, затем эхо. `TestRelayKeepsKeyOutOfArgvAndEnv`: дочерний процесс печатает `os.Args` и `os.Environ()`; ключа там нет.
- [ ] **Step 2:** Run — FAIL. **Step 3:** Реализовать (`exec.Cmd` с `StdinPipe`, `io.Copy` в горутинах). **Step 4:** Run — PASS. Commit: `"Add the keyed MCP relay to the veda binary"`.

### Task 4: Удаление Python-адаптера, релиз и документация

**Files:**
- Delete: `mcp/server.py`, `mcp/keyed_stdio_launcher.py`, `mcp/requirements.txt` перенести в `scripts/requirements-checks.txt` (только для проверок)
- Delete/port: `scripts/test_mcp_timeout.py`, `test_mcp_https.py`, `test_mcp_errors.py` (их проверки теперь в Task 1)
- Modify: `scripts/package_release.py` и `scripts/test_package_release.py` — без `mcp/`; добавить архив `darwin/arm64`; `scripts/smoke_fresh_systemd.sh` — проверка `veda mcp` вместо venv; `.github/workflows/ci.yml`; `README.md`, `docs/agent-workflow.md`, `AGENTS.md`

- [ ] **Step 1:** Run `rg -n "mcp/server.py|keyed_stdio_launcher|mcp/requirements" -g '!docs/superpowers/**'` — после правок пусто.
- [ ] **Step 2:** `scripts/test_package_release.py`: ожидание — архивы `linux-amd64`, `linux-arm64`, `darwin-arm64`, в архивах нет `mcp/`. Run — PASS.
- [ ] **Step 3:** `docs/agent-workflow.md`: конфигурации четырёх клиентов через `veda mcp` / `veda mcp-relay`; отдельный раздел «Переключение развёрнутого LXC161» — новый лаунчер `ssh pve 'pct exec 161 -- runuser -u infra-wiki -- /opt/veda-next/veda mcp --reader-socket /run/veda-next/reader.sock'`, старый `server.py` на сервере остаётся откатом.
- [ ] **Step 4:** Run `make test` и все внешние проверки — PASS. Commit: `"Remove the Python MCP adapter"`. Открыть PR.
