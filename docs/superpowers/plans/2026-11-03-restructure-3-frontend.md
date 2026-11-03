# Перестройка VEDA, фаза 3: фронтенд на TypeScript — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Перевести `web/` на TypeScript в строгом режиме и разложить по разделам без изменения внешнего вида и поведения.

**Architecture:** Сначала снимаются эталонные скриншоты. Затем инструменты (TypeScript, Vitest, ESLint), типы API и клиент. Дальше разделы переезжают из `App.jsx` по одному. CSS сначала механически режется по порядку подключения, и только потом части переносятся к разделам. На каждом шаге скриншоты сравниваются с эталоном.

**Tech Stack:** React 19.2, Vite 6.4, TypeScript (закреплённая версия), Vitest (закреплённая), ESLint 9 + `typescript-eslint` + `eslint-plugin-react-hooks` (закреплённые), Node 22.

**Spec:** `docs/superpowers/specs/2026-11-03-restructure-design.md`

## Global Constraints

- Фаза не зависит от фаз 1 и 2: путь встраивания UI берётся из `Makefile` той ветки, куда сливается.
- `tsconfig`: `"strict": true`, `"noImplicitAny": true`, `"noUncheckedIndexedAccess": true`; `any` запрещён правилом ESLint `@typescript-eslint/no-explicit-any`.
- Внешний вид не меняется: экраны гостевой главной, каталога (`?env=all`), статьи, графа (`?graph=1`), недавних (`?recent=1`), диалога входа, настроек доступа (`?settings=1`), GitHub (`?github=1`) на ширинах 1672, 390, 320; порог — 0,1% отличающихся пикселей на экран.
- Тексты интерфейса, классы CSS, `aria-label` и роли не меняются (на них опирается `scripts/smoke_ui.mjs`).
- Не больше 500 строк в файле `.ts`, `.tsx`, `.css` в `web/src` (включается в Task 6).
- Зависимости только закреплённых версий в `package.json`, `package-lock.json` обновлён.

## Review Focus

1. Ответ `/api/state` с отсутствующими необязательными полями (`GraphEdges: null`, `Cards: null`, `SourceWarnings: null`, пустые `Groups`) — тип допускает `null`, экран не падает — тест в Task 2.
2. 304 на опросе после перезапуска эффекта — данные сохраняются (поведение `statePoll`) — тест в Task 2.
3. Вход по ссылке с двумя параметрами `key` — ошибка входа, ключ убран из адреса — перенос `link-login.test` в Task 2.
4. Ошибка сети в диалогах доступа и GitHub — показывается прежнее сообщение, форма не зависает в `busy` — тест в Task 4.
5. Мобильное меню на 390 и 320 — открытие и закрытие по Escape — браузерный smoke в Task 6.

---

### Task 1: Эталонные скриншоты и инструмент сравнения

**Files:**
- Create: `scripts/ui_screens.mjs` (CDP, как `smoke_ui.mjs`: поднимает фикстуру из `smoke_ui.py`, снимает перечисленные экраны), `scripts/ui_compare.mjs` (сравнение PNG), `scripts/ui_compare.test.mjs`, `scripts/screens.py` (обёртка с фикстурой и входом синтетического администратора)
- Output: `web/screens/baseline/<экран>-<ширина>.png` (в `.gitignore`, снимается локально)

**Interfaces:**
- Produces: `python3 scripts/screens.py --out DIR`; `node scripts/ui_compare.mjs BASE DIR --threshold 0.001` — печатает экраны выше порога, код 1 при нарушении.

- [ ] **Step 1: Тест** `ui_compare.test.mjs`: два одинаковых PNG → 0; PNG с изменённым 1% пикселей → выше порога. Сравнение PNG без зависимостей: снимок через CDP `Page.captureScreenshot` в формате PNG, декодирование через `zlib.inflateSync` (несжатые фильтры PNG разобрать в скрипте) — либо добавить `pngjs` закреплённой версии как dev-зависимость (решение исполнителя).
- [ ] **Step 2:** Run — FAIL → реализовать → PASS.
- [ ] **Step 3:** Снять эталон на текущем `main`: `python3 scripts/screens.py --out web/screens/baseline`. Повторный снимок и сравнение с эталоном — 0 нарушений (проверка стабильности; если экран нестабилен — отключить анимации и мигание курсора в фикстуре).
- [ ] **Step 4:** Commit: `"Capture reference screenshots for the frontend restructure"`.

### Task 2: TypeScript, линтер, тесты, типы и клиент API

**Files:**
- Create: `web/tsconfig.json`, `web/eslint.config.js`, `web/vitest.config.ts`
- Rename: `web/vite.config.mjs` → `web/vite.config.ts`; `web/src/{tree,catalog,recent,statePoll,link-login,githubErrors,githubSourceId,sourceWarnings}.js` → `web/src/lib/*.ts`; их тесты → `web/src/lib/*.test.ts` (Vitest)
- Create: `web/src/api/types.ts`, `web/src/api/http.ts`, `web/src/api/state.ts`, `web/src/api/session.ts`, `web/src/api/access.ts`, `web/src/api/github.ts`, тесты `web/src/api/*.test.ts`
- Modify: `web/package.json` (скрипты `typecheck`, `lint`, `test`), `Makefile` (`make test` запускает `npm run typecheck && npm run lint && npm test`)

**Interfaces:**
- Produces:
  - `api/types.ts`: `StateView`, `StatePayload { view: StateView; html: string; toc: string }`, `Page`, `Group`, `Card`, `SourceWarning`, `GraphEdge`, `AccessKey`, `IssuedKey`, `WikiUser`, `WikiTeam`, `SourceChoice`, `GitHubAccount`, `GitHubConnection`, `SourceMirror` — поля и регистр как в JSON сервера (`Pages`, `path`, `updated_at` и т. д.)
  - `api/http.ts`: `class HttpError extends Error { status: number }`, `getJSON<T>(route: string, init?: RequestInit): Promise<T>`, `postJSON<T>(route: string, body: unknown): Promise<T>`
  - `api/state.ts`: `fetchState(fetcher, url, signal, etag)` (перенос `statePoll`), `shouldPoll(visibility, etag)`
  - `api/session.ts`: `sessionStatus(prefix)`, `login(prefix, credentials)`, `logout(prefix)`, `changePassword(...)`, `profile()`, `loginFromLink(location, history, fetcher)`
  - `api/access.ts`: `listSources()`, `listKeys()`, `createKey(...)`, `updateKey(...)`, `revokeKey(id)`, `listUsers()`, `saveUser(...)`, `listTeams()`, `saveTeam(...)`
  - `api/github.ts`: `listAccounts()`, `addAccount(...)`, `listConnections()`, `connect(...)`, `reconfigure(...)`, `sync(id)`, `pause(id, paused)`, `listMirrors()`, `configureMirror(...)`
- [ ] **Step 1: Тесты:** перенос всех 17 существующих тестов на Vitest без изменения ожиданий; `state.test.ts` — `StatePayload` с `GraphEdges: null`, `Cards: null`, `SourceWarnings: null` принимается; `http.test.ts` — 409 даёт `HttpError` со `status === 409`.
- [ ] **Step 2:** Run `npm test` — FAIL → перенести модули и написать клиент → PASS. `npm run typecheck` и `npm run lint` — PASS (JSX-файлы пока в `allowJs` и вне строгого линта; включаются по мере переноса).
- [ ] **Step 3:** Commit: `"Add TypeScript, Vitest, ESLint and a typed API client"`.

### Task 3: Оболочка, общие компоненты, чтение, каталог, главная, граф, недавние

**Files:**
- Create: `web/src/main.tsx`, `web/src/App.tsx` (выбор раздела по параметрам адреса, опрос состояния через `hooks/useStatePoll.ts`), `web/src/hooks/useStatePoll.ts`, `web/src/hooks/useShortcut.ts`, `web/src/components/{Icon,Panel,Sidebar,Header,FeatureDialog,SourceWarnings}.tsx`, `web/src/features/reading/{Reading,Toc}.tsx`, `web/src/features/catalog/{Catalog,CatalogFolders,Results,Tree}.tsx`, `web/src/features/home/{Dashboard,PageCards,BrowseCards,MorePages,Activity,Integrations,Statistics,Shortcuts}.tsx`, `web/src/features/graph/Graph.tsx` (+ `layout.ts` с вычислением позиций и тестом), `web/src/features/recent/Recent.tsx`
- Delete: `web/src/App.jsx`, `web/src/main.jsx`, `web/src/icon.jsx`

**Interfaces:**
- Consumes: `api/*` из Task 2.
- Produces: `useStatePoll(): { data: StatePayload | null; error: string }` с поведением нынешнего эффекта (ETag в ref, пауза в скрытой вкладке, перезапуск при закрытии диалога); `FeatureContext` для открытия диалогов.

- [ ] **Step 1: Тест** `graph/layout.test.ts`: позиции узлов для 1, 2, 3 и 12 страниц в группе совпадают с нынешними формулами из `Graph` (`x`, `y`, `width`).
- [ ] **Step 2:** Перенести компоненты без изменения разметки и классов.
- [ ] **Step 3:** Run `npm test && npm run typecheck && npm run lint && npm run build`, затем `python3 scripts/screens.py --out /tmp/screens && node scripts/ui_compare.mjs web/screens/baseline /tmp/screens --threshold 0.001` — 0 нарушений; `python3 scripts/smoke_ui.py` — PASS.
- [ ] **Step 4:** Commit: `"Move reading, catalog, home, graph and recent views to TypeScript"`.

### Task 4: Сессия, доступ, GitHub

**Files:**
- Create: `web/src/features/session/{ReaderAccess,LoginForm,PasswordForm}.tsx`, `web/src/features/access/{AccessSettings,UsersTab,TeamsTab,KeysTab,AccessModal,IssuedKey}.tsx`, `web/src/features/github/{GitHubConnect,Accounts,Connections,Mirrors,GitHubDialog}.tsx`, тесты `web/src/features/access/AccessSettings.test.tsx`, `web/src/features/github/GitHubConnect.test.tsx` (React Testing Library закреплённой версии, `jsdom`)
- Delete: `web/src/AccessSettings.jsx`, `web/src/GitHubConnect.jsx`

- [ ] **Step 1: Тесты:** `access keeps form usable after a network error` — `fetch` отклоняется при создании ключа → показано «Создание не подтверждено: ключ мог быть создан. Проверьте список перед повтором.», кнопка снова активна; `disable refusal explains last administrator` — 409 при `disabled` → текст про последнего активного администратора; `github shows login when session is missing` — статус `authenticated: false` → поля `username` и `password`.
- [ ] **Step 2:** Run — FAIL → перенести → PASS.
- [ ] **Step 3:** Скриншоты и `smoke_ui.py` как в Task 3 — без нарушений. Commit: `"Move session, access and GitHub administration to TypeScript"`.

### Task 5: CSS по разделам

**Files:**
- Create: `web/src/styles/tokens.css` (переменные `:root` из начала `styles.css`), `web/src/styles/fonts.css`, `web/src/styles/base.css`; файлы разделов рядом с компонентами (`components/shell.css`, `features/reading/reading.css`, …)
- Delete: `web/src/styles.css`

- [ ] **Step 1: Механическое разбиение.** Разрезать `styles.css` на файлы по непрерывным диапазонам строк (не меняя порядок правил) и подключить их из `main.tsx` в том же порядке. Скриншоты — 0 нарушений. Commit: `"Split the stylesheet in its original cascade order"`.
- [ ] **Step 2: Перенос к разделам.** По одному разделу: перенести его правила (включая поздние переопределения из блоков `/* Reference alignment ... */`, `/* Settings follow ... */` и т. п.) в файл раздела, сохраняя относительный порядок; заменить повторяющиеся цвета и отступы переменными из `tokens.css`. После каждого раздела — скриншоты, 0 нарушений; иначе откатить раздел и оставить его правила на месте.
- [ ] **Step 3:** Commit по каждому разделу: `"Move <раздел> styles next to its components"`.

### Task 6: Защита и документация

- [ ] **Step 1:** `Makefile` `lint`: `check_file_size.py --max 500 --include 'web/src/**/*.ts' --include 'web/src/**/*.tsx' --include 'web/src/**/*.css'`; ESLint без исключений для старых `.jsx` (их больше нет); `allowJs` выключен.
- [ ] **Step 2:** Run `make test`, `npm run build`, `python3 scripts/smoke_ui.py`, скриншоты — всё зелёное, 0 нарушений.
- [ ] **Step 3:** `README.md` (раздел разработки фронтенда: `npm run typecheck`, `lint`, `test`), `docs/architecture.md` (раздел фронтенда, если файл уже есть после фазы 1, иначе создать). Commit: `"Guard frontend structure and document it"`. Открыть PR.
