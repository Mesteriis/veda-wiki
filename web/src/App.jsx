import { useState, useEffect, useRef, createContext, useContext } from "react";
import { buildTree, buildMergedTree } from "./tree.js";
import { githubErrorMessage, githubVisibilityLabel } from "./githubErrors.js";
import { conflictWarning, observationLabel, syncWarning } from "./sourceWarnings.js";
const Features = createContext(() => {});
const descriptions = {
  github: [
    "GitHub",
    "Управление подключениями GitHub",
  ],
  graph: [
    "Граф знаний",
    "Граф знаний пока недоступен. Для навигации используйте структуру разделов и ссылки на страницах.",
  ],
  favorites: [
    "Избранное",
    "Избранное ещё не настроено. Можно пользоваться закладками браузера.",
  ],
  recent: [
    "История просмотров",
    "История просмотров пока не записывается. На главной показаны основные страницы wiki.",
  ],
  api: [
    "API и MCP",
    "Статьи создаются и обновляются через REST API или MCP. Веб-интерфейс показывает статьи и позволяет управлять подключениями GitHub; статьи хранятся локально.",
  ],
  settings: [
    "Настройки",
    "Приватный источник доступен только с авторизацией. Публичные статьи видны без входа.",
  ],
  notifications: [
    "Уведомления",
    "Внутренний центр уведомлений VEDA пока не настроен.",
  ],
  theme: [
    "Оформление",
    "Активна тёмная тема VEDA. Переключение тем пока недоступно.",
  ],
  profile: [
    "Профиль",
    "База знаний VEDA. Учётные записи и роли VEDA пока не настроены.",
  ],
};
const pageHref = (p) => "/?page=" + encodeURIComponent(p);
function updatedLabel(value) {
  const date = new Date(value);
  return value && !Number.isNaN(date.getTime())
    ? `Обновлено ${new Intl.DateTimeFormat("ru-RU", { dateStyle: "medium", timeStyle: "short" }).format(date)}`
    : "Время обновления неизвестно";
}
function I({ name, ...rest }) {
  return (
    <img
      className="icon"
      src={"/assets/icons/" + name + ".svg"}
      alt=""
      aria-hidden="true"
      {...rest}
    />
  );
}
function F({ name, children, className = "", label }) {
  const open = useContext(Features);
  return (
    <button className={className} onClick={() => open(name)} aria-label={label}>
      {children}
    </button>
  );
}
function Panel({ title, children, className = "", actions }) {
  return (
    <section className={"panel " + className}>
      <div className="section-heading">
        <h2>{title}</h2>
        {actions}
      </div>
      {children}
    </section>
  );
}
function SourceWarnings({ view }) {
  const warnings = (view.SourceWarnings || []).filter((source) => !view.Page || source.ID === view.Page.environment);
  if (!warnings.length) return null;
  return <section className="source-warnings" aria-label="Состояние источников">
    {warnings.map((source) => <div className="source-warning" key={source.ID} role="status">
      <strong>{source.Name}:</strong>{" "}
      {[source.Partial && "импорт неполный", source.Withheld > 0 && `удержано или недоступно: ${source.Withheld} (возможны повторы)`, source.Dirty && "в источнике есть изменения вне коммита", source.Conflicts > 0 && `конфликты: ${source.Conflicts}`, syncWarning(source.SyncIssue), observationLabel(source.LastImportObservedAt)].filter(Boolean).join(" · ")}.
      {" "}{source.SyncIssue && "Локальная копия может быть неактуальна. "}Проверяйте актуальное состояние перед действиями.
    </div>)}
  </section>;
}
function Sidebar({ v }) {
  const [menuOpen, setMenuOpen] = useState(false);
  const nav = [
    ["home", "Главная", "/"],
    ["search", "Поиск", "/?q=документация"],
    ["notes", "Все страницы", "/?env=all"],
    ["affiliate", "Граф знаний", null, "graph"],
    ["star", "Избранное", null, "favorites"],
    ["clock", "Недавние", null, "recent"],
    ["brand-github", "GitHub", null, "github"],
    ["api", "API и MCP", null, "api"],
    ["settings", "Настройки", null, "settings"],
  ];
  return (
    <aside className="sidebar" onKeyDown={(event) => { if (event.key === "Escape" && menuOpen) setMenuOpen(false); }}>
      <a className="brand" href="/">
        <img src="/assets/veda-logo.png" alt="" className="brand-mark" />
        <span>
          <strong>VEDA</strong>
          <small>KNOWLEDGE BASE</small>
        </span>
      </a>
      <nav id="main-navigation" className={menuOpen ? "mobile-open" : ""} aria-label="Основная навигация" onClick={(event) => { if (event.target.closest(".nav-item")) setMenuOpen(false); }}>
        {nav.filter(([, , , feature]) => !v.Public || !['github', 'api', 'settings', 'favorites'].includes(feature)).map(([icon, label, href, feature]) =>
          href ? (
            <a
              className={
                "nav-item " +
                (href === "/" && !v.Query && !v.Selected && !v.Page
                  ? "selected"
                  : "")
              }
              href={href}
              key={label}
            >
              <I name={icon} />
              <span>{label}</span>
            </a>
          ) : (
            <F name={feature} className="nav-item" key={label}>
              <I name={icon} />
              <span>{label}</span>
            </F>
          ),
        )}
      </nav>
      <button type="button" className="mobile-menu-toggle" aria-label="Меню" aria-controls="main-navigation" aria-expanded={menuOpen} onClick={() => setMenuOpen(!menuOpen)}>
        <I name={menuOpen ? "x" : "dots"} />
      </button>
      {!v.Public && <div className="sidebar-bottom">
        <section className="storage panel">
          <div>
            Хранилище (Markdown)
            <I name="git-branch" />
          </div>
          <p>
            <b>{v.Count}</b> страниц<span>Локально</span>
          </p>
          <div className="meter">
            <i />
          </div>
        </section>
        <section className="system panel">
          <div className="system-brand">
            <img src="/assets/veda-logo.png" alt="" />
            <div>
              VEDA Wiki<small>v0.1.0</small>
            </div>
            <I name="chevron-down" />
          </div>
          <dl>
            {[
              ["shield-check", "Wiki", true],
              ["shield-check", "MCP", false],
              ["brand-github", "GitHub", v.GitHubCount > 0],
            ].map(([icon, name, active]) => (
              <div key={name}>
                <dt>
                  <I name={icon} />
                  {name}
                </dt>
                <dd className={active ? "" : "muted"}>
                  {active && <i />}
                  {name === "MCP" ? "Неизвестно" : active ? (name === "GitHub" ? "Настроен" : "Online") : "Не настроен"}
                </dd>
              </div>
            ))}
          </dl>
        </section>
      </div>}
    </aside>
  );
}
function Header({ v }) {
  return (
    <header className="masthead">
      <div className="masthead-title">
        <h1>Вики</h1>
        <p>Ваше единое место для знаний, проектов и документации</p>
      </div>
      <form action="/" className="global-search" role="search">
        <label className="sr" htmlFor="search">
          Поиск по вики
        </label>
        <I name="search" />
        <input
          id="search"
          name="q"
          placeholder="Поиск по вики..."
          defaultValue={v.Query}
        />
        <kbd>⌘</kbd>
        <kbd>K</kbd>
      </form>
      <div className="header-actions">
        {!v.Public && <><span className="reading-mode"><I name="shield-check" />Только чтение</span>
        <F className="round" name="notifications" label="Уведомления">
          <I name="bell" />
        </F>
        <F className="round" name="theme" label="Оформление">
          <I name="sun" />
        </F></>}
        {v.Public ? <F className="login-button" name="profile" label="Войти">Войти</F> : <F className="round profile" name="profile" label="Профиль">V</F>}
      </div>
    </header>
  );
}
function BrowseCards() {
  return (
    <section className="browse-cards" aria-label="Навигация по базе знаний">
      <a className="browse-card" href="/?env=all">
        <span className="tile"><I name="notes" /></span>
        <strong>Библиотека</strong><small>Все статьи базы знаний</small>
      </a>
      <a className="browse-card" href="/?q=документация">
        <span className="tile"><I name="search" /></span>
        <strong>Найти ответ</strong><small>Поиск по документации</small>
      </a>
      <a className="browse-card" href="#knowledge-tree">
        <span className="tile"><I name="folder" /></span>
        <strong>Разделы</strong><small>Источники документации</small>
      </a>
    </section>
  );
}
function PageCards({ cards }) {
  return (
    <Panel
      title="Недавно обновлённые"
      className="recent-pages"
      actions={
        <a href="/?env=all">
          Смотреть все <I name="arrow-right" />
        </a>
      }
    >
      <div className="recent-grid">
        {cards.map((c) => (
          <a
            className="page-card"
            key={c.Page.path}
            href={pageHref(c.Page.path)}
          >
            <img className="page-cover" src={c.ImageURL || "/assets/" + c.Image} alt="" />
            <div className="page-card-copy">
              <strong title={c.Page.title}>{c.Page.title}</strong>
              <small className="card-path">/{c.Page.path}</small>
              <p>{c.Description}</p>
              <div className="tags">
                <span>#{c.Tag}</span>
                <span>#wiki</span>
              </div>
              <div className="card-meta">
                <I name="clock" />
                <time dateTime={c.Page.updated_at || undefined}>{updatedLabel(c.Page.updated_at)}</time>
              </div>
            </div>
          </a>
        ))}
      </div>
    </Panel>
  );
}
function Tree({ groups }) {
  const [mode, setMode] = useState("merged");
  return (
    <Panel
      title="Структура знания"
      className="knowledge-tree"
      actions={
        <>
          {groups.length > 1 && <div className="tabs" aria-label="Вид структуры">
            <button type="button" className={mode === "sources" ? "active" : ""} aria-pressed={mode === "sources"} onClick={() => setMode("sources")}>Источники</button>
            <button type="button" className={mode === "merged" ? "active" : ""} aria-pressed={mode === "merged"} onClick={() => setMode("merged")}>Сводно</button>
          </div>}
        </>
      }
    >
      <a className="tree-home" id="knowledge-tree" href="/">
        <I name="chevron-right" />
        <I name="home" />
        Главная
      </a>
      {mode === "merged" && groups.length > 1 ? <div className="tree-pages"><TreeNodes node={buildMergedTree(groups)} /></div> : groups.map((g) => (
        <details className="tree-group" key={g.ID}>
          <summary>
            <I name="chevron-right" />
            <I name="folder" />
            <span>{g.Name}</span>
            <small>{g.Count}</small>
          </summary>
          <div className="tree-pages">
            {g.Description && <p className="tree-source-description">{g.Description}</p>}
            {g.SourceURL && <a className="tree-source-link" href={g.SourceURL} target="_blank" rel="noopener noreferrer">Сайт источника <I name="external-link" /></a>}
            <TreeNodes node={buildTree(g.ID, g.Pages || [])} />
            <a className="tree-all" href={"/?env=" + encodeURIComponent(g.ID)}>
              Все страницы раздела <I name="arrow-right" />
            </a>
          </div>
        </details>
      ))}
    </Panel>
  );
}
function TreeNodes({ node }) {
  const folders = [...node.folders.entries()].sort(([a], [b]) => a.localeCompare(b));
  const pages = [...node.pages].sort((a, b) => a.title.localeCompare(b.title) || a.path.localeCompare(b.path));
  return (
    <>
      {folders.map(([name, child]) => (
        <details className="tree-group tree-folder" key={name}>
          <summary><I name="chevron-right" /><I name="folder" /><span title={name}>{name}</span></summary>
          <div className="tree-pages"><TreeNodes node={child} /></div>
        </details>
      ))}
      {pages.map((page) => (
        <a href={pageHref(page.path)} key={page.path} title={page.path}>
          <I name="file-text" /><span>{page.title}</span>{page.sourceID && <small title={page.sourceID}>{page.sourceName} · {page.sourceID}</small>}
        </a>
      ))}
    </>
  );
}
function MorePages({ cards, pages }) {
  const displayed = new Set(cards.map((card) => card.Page.path));
  const more = pages.filter((page) => !displayed.has(page.path)).slice(0, 5);
  return (
    <Panel
      title="Другие страницы"
      className="popular"
      actions={<a href="/?env=all">Все страницы <I name="arrow-right" /></a>}
    >
      {more.length ? <div className="popular-list">{more.map((page) => (
        <a href={pageHref(page.path)} key={page.path}>
          <span className="tiny-tile"><I name="file-description" /></span>
          <span className="popular-label"><strong>{page.title}</strong><small>/{page.path}</small></span>
        </a>
      ))}</div> : <p className="data-note">Других страниц пока нет.</p>}
    </Panel>
  );
}
function Activity() {
  const [tab, setTab] = useState("Все");
  return (
    <Panel
      title="Недавняя активность"
      className="activity"
      actions={
        <div className="tabs activity-tabs">
          {["Все", "GitHub", "API", "MCP"].map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={t === tab ? "active" : ""}
            >
              {t}
            </button>
          ))}
        </div>
      }
    >
      <div className="activity-items">
        <span className="tiny-tile">
          <I name="refresh" />
        </span>
        <p>
          {tab === "Все"
            ? "База знаний загружена."
            : tab === "MCP"
              ? "MCP работает через отдельный stdio-клиент. История вызовов не записывается."
              : `Активность ${tab} пока не подключена.`}
          <small>
            {tab === "Все"
              ? "Версии статей проверяются по SHA256"
              : "Данные появятся после настройки"}
          </small>
        </p>
        <span className="activity-time">Синхронизация</span>
      </div>
    </Panel>
  );
}
function Integrations({ isPublic, githubCount }) {
  const integrations = [
    [
      "brand-github",
      "GitHub",
      githubCount ? `${githubCount} источников` : "Источники документации",
      "Хранение контента в Markdown",
      "github",
      githubCount > 0,
    ],
    [
      "link",
      "MCP Server",
      isPublic ? "Приватный доступ" : "stdio · REST API",
      "Доступ к вики для AI агентов",
      "api",
      false,
    ],
    [
      "api",
      "REST API",
      "/api/state",
      "Доступ к контенту для веб-интерфейса",
      "api",
      true,
    ],
  ];
  return (
    <Panel title="Источники знаний" className="integrations">
      {integrations.map(([icon, name, source, detail, feature, active]) => (
        <div className="integration-row" key={name}>
          <span className={"integration-icon " + feature}>
            <I name={icon} />
          </span>
          <div>
            <strong>{name}</strong>
            <small>{source}</small>
            <p>{detail}</p>
          </div>
          <span className={"status " + (!active ? "pending" : "")}>
            {active
              ? name === "GitHub" ? "Настроен" : name === "REST API"
                ? "Чтение"
                : "Подключён"
              : name === "MCP Server" ? "Неизвестно" : "Не настроен"}
          </span>
          <F name={feature} label={"Настроить " + name}>
            <I name="settings" />
          </F>
        </div>
      ))}
    </Panel>
  );
}
function Statistics({ count, groups, isPublic }) {
  return (
    <Panel title="База знаний" className="statistics">
      <div className="stat-grid">
        {[
          ["file-text", count, "Страниц"],
          ["folder", groups, "Разделов"],
          ["shield-check", isPublic ? "Public" : "Private", "Поверхность"],
        ].map(([icon, value, label]) => (
          <div key={label}>
            <I name={icon} />
            <span>
              <b>{value}</b>
              <small>{label}</small>
            </span>
          </div>
        ))}
      </div>
    </Panel>
  );
}
function Shortcuts() {
  return (
    <Panel title="Быстрые действия" className="shortcuts">
      <a href="/?q=документация">
        <I name="search" />
        <span>Поиск по вики</span>
        <kbd>⌘ K</kbd>
      </a>
      <F name="api">
        <I name="api" />
        <span>Задать вопрос AI (MCP)</span>
        <kbd>⌘ /</kbd>
      </F>
      <F name="github">
        <I name="brand-github" />
        <span>Подключение GitHub</span>
        <I name="refresh" />
      </F>
      <F name="settings">
        <I name="settings" />
        <span>Настроить интеграции</span>
      </F>
    </Panel>
  );
}
function Dashboard({ v }) {
  return (
    <div className="dashboard">
      <div className="center-column">
        <BrowseCards />
        {v.Count === 0 ? <Panel title="База знаний пока пуста"><p className="data-note">Добавьте источник и статьи через REST API или MCP. Веб-интерфейс предназначен для чтения.</p></Panel> : <PageCards cards={v.Cards || []} />}
        <div className="lower-grid">
          <Tree groups={v.Groups || []} />
          <div className="lower-right">
            <MorePages cards={v.Cards || []} pages={v.Pages || []} />
            {!v.Public && <Activity />}
          </div>
        </div>
      </div>
      <aside className="right-column">
        {!v.Public && <Integrations isPublic={v.Public} githubCount={v.GitHubCount || 0} />}
        <Statistics count={v.Count} groups={(v.Groups || []).length} isPublic={v.Public} />
        {!v.Public && <Shortcuts />}
      </aside>
    </div>
  );
}
function Reading({ data }) {
  const v = data.view;
  const warning = conflictWarning(v.Page);
  return (
    <div className="reading-layout">
      <article className="panel reading">
        <a className="back-link" href="/">
          Главная
        </a>
        <div className="source-path">{v.Page.path}</div>
        {warning && <p className="conflict-warning" role="status">{warning}</p>}
        <div className="provenance">
          {v.Page.translated ? "Перевод на русский · SHA256" : v.Page.translation_state === "stale" ? "Оригинал · перевод устарел · SHA256" : v.Page.program_documentation ? "Оригинал · перевод недоступен · SHA256" : "SHA256"} <code>{v.Page.sha256}</code>
          {v.Page.translated && (v.Page.translation_origin === "local_model" ? <> · Локальный автоматический перевод</> : <> · Происхождение перевода неизвестно</>)}
          {v.Page.translated && <> · Оригинал <code>{v.Page.original_sha256}</code></>}
        </div>
        <div
          className="markdown"
          dangerouslySetInnerHTML={{ __html: data.html }}
        />
        <footer>
          Версия: {v.Generated || "пустая база"}. Проверяйте дату и происхождение документации перед её использованием.
        </footer>
      </article>
      <aside className="toc panel">
        <h2>На этой странице</h2>
        <div dangerouslySetInnerHTML={{ __html: data.toc }} />
      </aside>
    </div>
  );
}
function Results({ v }) {
  return (
    <Panel
      className="search-results"
      title={v.Query ? "Результаты поиска: " + v.Query : "Все страницы"}
      actions={<span>{(v.Pages || []).length}</span>}
    >
      <div className="document-list">
        {(v.Pages || []).map((p) => (
          <a href={pageHref(p.path)} key={p.path}>
            <I name="file-text" />
            <span>
              <strong>{p.title}</strong>
              <small>{p.path}</small>
            </span>
            <I name="arrow-up-right" />
          </a>
        ))}
        {!v.Pages?.length && <p>Совпадений нет. Попробуйте другой запрос.</p>}
      </div>
    </Panel>
  );
}
function ReaderAccess() {
  const [status, setStatus] = useState(null);
  const [message, setMessage] = useState("");
  const [mode, setMode] = useState("user");
  const [canChangePassword, setCanChangePassword] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    fetch("/api/session/status", { signal: controller.signal })
      .then((r) => { if (!r.ok) throw Error(); return r.json(); })
      .then(async (result) => { setStatus(result.authenticated); if (result.authenticated) { const profile = await fetch("/api/session/profile"); if (profile.ok) setCanChangePassword((await profile.json()).password_change); } })
      .catch((e) => { if (e.name !== "AbortError") setMessage("Вход доступен на приватном адресе через HTTPS или на этом компьютере."); });
    return () => controller.abort();
  }, []);
  async function submit(event) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const credentials = mode === "user" ? { username: data.get("username"), password: data.get("password") } : { key: data.get("key") };
    setMessage("");
    try {
      const response = await fetch("/api/session/login", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(credentials) });
      if (!response.ok) throw Error();
      window.location.reload();
    } catch { setMessage("Не удалось войти. Проверьте данные и адрес подключения."); }
    finally { form.reset(); }
  }
  async function changePassword(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    setMessage("");
    if (data.get("new_password") !== data.get("confirm_password")) { setMessage("Новые пароли не совпадают."); return; }
    try {
      const response = await fetch("/api/session/password", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ current_password: data.get("current_password"), new_password: data.get("new_password") }) });
      if (!response.ok) throw Error();
      window.location.assign("/");
    } catch { setMessage("Не удалось изменить пароль. Проверьте текущий пароль и повторите попытку."); }
    finally { form.reset(); }
  }
  async function logout() {
    try {
      const response = await fetch("/api/session/logout", { method: "POST" });
      if (!response.ok) throw Error();
      window.location.assign("/");
    } catch { setMessage("Не удалось завершить сеанс."); }
  }
  return (
    <div className="reader-access">
      {status === true ? <><p>Приватное чтение включено. Сеанс действует до 8 часов.</p>{canChangePassword && <form onSubmit={changePassword}><h3>Изменить пароль</h3><label>Текущий пароль<input name="current_password" type="password" autoComplete="current-password" required /></label><label>Новый пароль<input name="new_password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><label>Повторите новый пароль<input name="confirm_password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><button>Изменить пароль</button></form>}<button onClick={logout}>Выйти</button></> : status === false ?
        <><button type="button" onClick={() => { setMode(mode === "user" ? "key" : "user"); setMessage(""); }}>{mode === "user" ? "Войти по ключу" : "Войти по имени"}</button><form key={mode} onSubmit={submit}>{mode === "user" ? <><p>Войдите в свою учётную запись.</p><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Пароль<input name="password" type="password" autoComplete="current-password" required /></label></> : <><p>Введите ключ чтения.</p><label>Ключ чтения<input name="key" type="password" autoComplete="off" required /></label></>}<button type="submit">Войти</button></form></> : null}
      {message && <p role="alert">{message}</p>}
    </div>
  );
}
function AccessSettings() {
  const [status, setStatus] = useState(null);
  const [sharedSession, setSharedSession] = useState(false);
  const [sources, setSources] = useState([]);
  const [keys, setKeys] = useState([]);
  const [teams, setTeams] = useState([]);
  const [users, setUsers] = useState([]);
  const [issued, setIssued] = useState(null);
  const [editing, setEditing] = useState(null);
  const [editingTeam, setEditingTeam] = useState(null);
  const [editingUser, setEditingUser] = useState(null);
  const [resettingUser, setResettingUser] = useState(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function json(route, options = {}) {
    const response = await fetch(route, options);
    if (!response.ok) { const error = Error("request failed"); error.status = response.status; throw error; }
    return response.json();
  }
  async function refresh() {
    const [available, existing, groups, people] = await Promise.all([json("/api/access/sources"), json("/api/access/keys"), json("/api/access/teams"), json("/api/access/users")]);
    setSources(available); setKeys(existing); setTeams(groups); setUsers(people);
  }
  async function send(route, body) {
    return json(route, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  }
  useEffect(() => {
    const controller = new AbortController();
    json("/api/access/session/status", { signal: controller.signal })
      .then(async (s) => { setStatus(s.authenticated); setSharedSession(!!s.via_reader_session); if (s.authenticated) await refresh(); })
      .catch((e) => { if (e.name !== "AbortError") setMessage("Управление доступно на приватном адресе через HTTPS или на этом компьютере."); });
    return () => controller.abort();
  }, []);
  async function login(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form); const key = data.get("key");
    setBusy(true); setMessage("");
    try { await send("/api/access/session/login", key ? { key } : { username: data.get("username"), password: data.get("password") }); setStatus(true); await refresh(); }
    catch { setMessage("Не удалось войти или загрузить источники. Проверьте подключение."); }
    finally { form.reset(); setBusy(false); }
  }
  async function create(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    const days = Number(data.get("days")); const selected = data.getAll("sources");
    if (!Number.isInteger(days) || days < 1 || days > 365 || !selected.length) { setMessage("Выберите источники и срок от 1 до 365 дней."); return; }
    setBusy(true); setMessage(""); setIssued(null);
    try {
      const result = await send("/api/access/keys", { id: data.get("id"), sources: selected, expires_at: new Date(Date.now() + days * 86400000).toISOString() });
      setIssued(result); form.reset();
      try { await refresh(); } catch { setMessage("Ключ создан, но список не обновился. Сохраните ключ и обновите список позже."); }
    } catch { setMessage("Создание не подтверждено: ключ мог быть создан. Проверьте список перед повтором."); }
    finally { setBusy(false); }
  }
  async function revoke(id) {
    if (!window.confirm(`Отозвать ключ ${id}? Доступ по нему прекратится.`)) return;
    setBusy(true); setMessage("");
    try { await send("/api/access/keys/revoke", { id }); await refresh(); }
    catch { setMessage("Не удалось подтвердить отзыв. Обновите список перед повтором."); }
    finally { setBusy(false); }
  }
  async function update(event, key) {
    event.preventDefault();
    const selected = new FormData(event.currentTarget).getAll("sources");
    if (!selected.length) { setMessage("Выберите хотя бы один источник."); return; }
    setBusy(true); setMessage("");
    try {
      await send("/api/access/keys", { id: key.id, sources: selected, expires_at: key.expires_at, expected_etag: key.etag });
      setEditing(null);
      try { await refresh(); } catch { setMessage("Права изменены, но список не обновился. Обновите его позже."); }
    } catch (error) {
      setMessage(error.status === 409 ? "Ключ изменился или срок истёк. Обновите список и проверьте права." : "Изменение не подтверждено. Обновите список перед повтором.");
    } finally { setBusy(false); }
  }
  async function saveTeam(event, team = null) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form); const selected = data.getAll("sources");
    if (!selected.length) { setMessage("Выберите хотя бы один источник для команды."); return; }
    const body = { id: team?.id || data.get("id"), sources: selected };
    if (team) body.expected_revision = team.revision;
    setBusy(true); setMessage("");
    try { await send("/api/access/teams", body); setEditingTeam(null); if (!team) form.reset(); await refresh(); }
    catch (error) { setMessage(error.status === 409 ? "Команда изменилась. Обновите список и проверьте права." : "Не удалось подтвердить изменение команды. Обновите список перед повтором."); }
    finally { setBusy(false); }
  }
  async function createUser(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setMessage("");
    try { await send("/api/access/users", { id: data.get("id"), password: data.get("password"), teams: data.getAll("teams") }); form.reset(); await refresh(); }
    catch { form.reset(); setMessage("Создание не подтверждено. Проверьте список пользователей перед повтором."); }
    finally { setBusy(false); }
  }
  async function updateUser(user, changes) {
    setBusy(true); setMessage("");
    try { await send("/api/access/users", { id: user.id, expected_revision: user.revision, ...changes }); setEditingUser(null); setResettingUser(null); await refresh(); }
    catch (error) { setMessage(error.status === 409 ? "Пользователь изменился. Обновите список и повторите действие." : "Изменение не подтверждено. Обновите список перед повтором."); }
    finally { setBusy(false); }
  }
  function updateMembership(event, user) { event.preventDefault(); updateUser(user, { teams: new FormData(event.currentTarget).getAll("teams") }); }
  function resetPassword(event, user) { event.preventDefault(); const form = event.currentTarget; const password = new FormData(form).get("password"); form.reset(); updateUser(user, { password }); }
  async function logout() {
    setBusy(true); setMessage("");
    try { await send("/api/access/session/logout", {}); setStatus(false); setKeys([]); setSources([]); setTeams([]); setUsers([]); setIssued(null); setEditing(null); setEditingTeam(null); setEditingUser(null); setResettingUser(null); }
    catch { setMessage("Не удалось завершить сеанс."); }
    finally { setBusy(false); }
  }
  return <div className="github-connect reader-access">
    <p>Ключи дают только чтение выбранных источников. Для агентов и пользователей можно выдавать отдельные ключи.</p>
    {status === false && <><form onSubmit={login}><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Пароль<input name="password" type="password" autoComplete="current-password" required /></label><button disabled={busy}>Войти в управление доступом</button></form><details><summary>Вход с ключом администратора</summary><form onSubmit={login}><label>Ключ администратора<input name="key" type="password" autoComplete="off" required /></label><button disabled={busy}>Войти</button></form></details></>}
    {status === true && <>
      <form onSubmit={saveTeam}><h3>Новая команда</h3><label>Идентификатор<input name="id" required placeholder="team-ops" /></label><fieldset><legend>Источники команды</legend>{sources.map((s) => <label className="access-source" key={s.id}><input type="checkbox" name="sources" value={s.id} /> {s.title} ({s.id})</label>)}</fieldset><button disabled={busy || !sources.length}>Создать команду</button></form>
      <h3>Команды</h3>{teams.length ? <ul>{teams.map((team) => <li key={team.id}><strong>{team.id}</strong> · {team.sources.join(", ")} <button type="button" disabled={busy} onClick={() => setEditingTeam(editingTeam === team.id ? null : team.id)}>Изменить источники</button>{editingTeam === team.id && <form onSubmit={(event) => saveTeam(event, team)}><fieldset><legend>Источники для {team.id}</legend>{sources.map((s) => <label className="access-source" key={s.id}><input type="checkbox" name="sources" value={s.id} defaultChecked={team.sources.includes(s.id)} /> {s.title} ({s.id})</label>)}</fieldset><button disabled={busy}>Сохранить команду</button></form>}</li>)}</ul> : <p>Команд пока нет.</p>}
      <form onSubmit={createUser}><h3>Новый пользователь</h3><label>Имя пользователя<input name="id" required placeholder="reader-one" /></label><label>Начальный пароль<input name="password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><p>Передайте начальный пароль безопасно; пользователь сможет его сменить.</p><fieldset><legend>Команды пользователя</legend>{teams.map((team) => <label className="access-source" key={team.id}><input type="checkbox" name="teams" value={team.id} /> {team.id}</label>)}</fieldset><button disabled={busy}>Создать пользователя</button></form>
      <h3>Пользователи</h3>{users.length ? <ul>{users.map((user) => <li key={user.id}><strong>{user.id}</strong> · {user.admin ? "Администратор" : "Читатель"} · {user.disabled ? "Отключён" : "Активен"} · {user.teams.join(", ") || "без команд"} <button type="button" disabled={busy} onClick={() => setEditingUser(editingUser === user.id ? null : user.id)}>Изменить команды</button> <button type="button" disabled={busy} onClick={() => setResettingUser(resettingUser === user.id ? null : user.id)}>Сменить пароль</button> <button type="button" disabled={busy} onClick={() => updateUser(user, { disabled: !user.disabled })}>{user.disabled ? "Включить" : "Отключить"}</button>{editingUser === user.id && <form onSubmit={(event) => updateMembership(event, user)}><fieldset><legend>Команды для {user.id}</legend>{teams.map((team) => <label className="access-source" key={team.id}><input type="checkbox" name="teams" value={team.id} defaultChecked={user.teams.includes(team.id)} /> {team.id}</label>)}</fieldset><button disabled={busy}>Сохранить команды</button></form>}{resettingUser === user.id && <form onSubmit={(event) => resetPassword(event, user)}><label>Новый пароль для {user.id}<input name="password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><button disabled={busy}>Сменить пароль</button></form>}</li>)}</ul> : <p>Пользователей пока нет.</p>}
      {issued && <div className="access-issued" role="status"><strong>Ключ {issued.id} создан</strong><p>Сохраните его сейчас: повторно VEDA его не покажет.</p><textarea readOnly value={issued.key} aria-label="Новый ключ чтения" rows="3" onFocus={(e) => e.target.select()} /><button type="button" onClick={() => setIssued(null)}>Скрыть ключ</button></div>}
      <form onSubmit={create}><h3>Новый ключ чтения</h3><label>Идентификатор<input name="id" required placeholder="agent-one" /></label><label>Срок в днях<input name="days" type="number" min="1" max="365" defaultValue="30" required /></label><fieldset><legend>Доступные источники</legend>{sources.length ? sources.map((s) => <label className="access-source" key={s.id}><input type="checkbox" name="sources" value={s.id} /> {s.title} ({s.id})</label>) : <p>Источников пока нет.</p>}</fieldset><button disabled={busy || !!issued || !sources.length}>Создать ключ</button></form>
      <h3>Выданные ключи</h3>{keys.length ? <ul>{keys.map((k) => <li key={k.id}><strong>{k.id}</strong> · {k.sources.join(", ")} · до {new Date(k.expires_at).toLocaleString()} <button type="button" disabled={busy} onClick={() => setEditing(editing === k.id ? null : k.id)}>Изменить доступ</button> <button type="button" disabled={busy} onClick={() => revoke(k.id)}>Отозвать</button>{editing === k.id && <form onSubmit={(event) => update(event, k)}><fieldset><legend>Источники для {k.id}</legend>{sources.map((s) => <label className="access-source" key={s.id}><input type="checkbox" name="sources" value={s.id} defaultChecked={k.sources.includes(s.id)} /> {s.title} ({s.id})</label>)}</fieldset><button disabled={busy}>Сохранить доступ</button> <button type="button" disabled={busy} onClick={() => setEditing(null)}>Отмена</button></form>}</li>)}</ul> : <p>Ключей пока нет.</p>}
      {sharedSession ? <p>Вход выполнен через учётную запись. Для выхода используйте профиль.</p> : <button type="button" disabled={busy} onClick={logout}>Выйти из управления доступом</button>}
    </>}
    {message && <p role="alert">{message}</p>}
  </div>;
}
function GitHubConnect() {
  const stateLabels = { pending: "Ожидает синхронизации", syncing: "Синхронизация", ready: "Синхронизирован", paused: "Приостановлен", skipped: "Нет папки docs/", blocked: "Требует внимания", error: "Ошибка синхронизации" };
  const [status, setStatus] = useState(null);
  const [sharedSession, setSharedSession] = useState(false);
  const [accounts, setAccounts] = useState([]);
  const [connections, setConnections] = useState([]);
  const [mirrors, setMirrors] = useState([]);
  const [accountKind, setAccountKind] = useState("app");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function json(route, options = {}) {
    const response = await fetch(route, options);
    if (!response.ok) {
      const body = await response.json().catch(() => ({}));
      const error = new Error("request failed"); error.code = body.error; error.status = response.status; throw error;
    }
    return response.json();
  }
  async function refresh() {
    const [a, c, m] = await Promise.all([json("/api/admin/github/accounts"), json("/api/admin/github/connections"), json("/api/admin/github/mirrors")]);
    setAccounts(a); setConnections(c); setMirrors(m);
  }
  useEffect(() => {
    const controller = new AbortController();
    json("/api/admin/session/status", { signal: controller.signal })
      .then(async (s) => { setStatus(s.authenticated); setSharedSession(!!s.via_reader_session); if (s.authenticated) await refresh(); })
      .catch((e) => { if (e.name !== "AbortError") setMessage("Управление доступно на приватном адресе через HTTPS или на этом компьютере."); });
    return () => controller.abort();
  }, []);
  useEffect(() => {
    if (!status) return;
    const timer = setInterval(() => { refresh().catch(() => setMessage("Не удалось обновить состояние GitHub.")); }, 10000);
    return () => clearInterval(timer);
  }, [status]);
  async function send(route, data) {
    return json(route, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(data) });
  }
  async function login(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form); const key = data.get("key");
    setBusy(true); setMessage("");
    try { await send("/api/admin/session/login", key ? { key } : { username: data.get("username"), password: data.get("password") }); setStatus(true); refresh().catch(() => setMessage("Вход выполнен, но не удалось загрузить настройки.")); }
    catch { setMessage("Не удалось войти. Проверьте данные и подключение."); }
    finally { form.reset(); setBusy(false); }
  }
  async function setup(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    if (data.get("password") !== data.get("confirm")) { setMessage("Пароли не совпадают."); return; }
    setBusy(true); setMessage("");
    try { await send("/api/admin/session/setup", { username: data.get("username"), setup_token: data.get("token"), password: data.get("password") }); setMessage("Пароль установлен. Теперь войдите с именем пользователя и паролем."); form.reset(); }
    catch { setMessage("Не удалось установить пароль. Проверьте код, срок его действия и пароль."); }
    finally { setBusy(false); }
  }
  async function addAccount(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setMessage("");
    const secret = accountKind === "app"
      ? { kind: "app", app_id: Number(data.get("app_id")), installation_id: Number(data.get("installation_id")), private_key: data.get("private_key") }
      : { kind: "token", token: data.get("token") };
    try { await send("/api/admin/github/accounts", { id: data.get("id"), ...secret }); form.reset(); setMessage("Аккаунт подключён."); refresh().catch(() => setMessage("Аккаунт подключён, но не удалось обновить список.")); }
    catch (e) { setMessage(githubErrorMessage(e.code, e.status, "Не удалось подключить аккаунт. Проверьте данные и идентификатор.")); }
    finally { if (form.elements.token) form.elements.token.value = ""; if (form.elements.private_key) form.elements.private_key.value = ""; setBusy(false); }
  }
  async function connect(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    const account = data.get("account");
    setBusy(true); setMessage("");
    try {
      await send("/api/admin/github/connections", { id: data.get("id"), repository: data.get("repository"), account_id: account, branch: data.get("branch"), write: data.has("write"), adopt_existing: data.has("adopt_existing") });
      const adopted = data.has("adopt_existing"); form.reset(); setMessage(adopted ? "Источник привязан. Проверьте конфликты и отключите прежний локальный импорт." : "Репозиторий подключён; импорт выполняется в фоне."); refresh().catch(() => setMessage("Репозиторий подключён, но не удалось обновить список."));
    } catch (e) { setMessage(githubErrorMessage(e.code, e.status, "Не удалось подключить репозиторий. Проверьте адрес, ветку и права аккаунта.")); }
    finally { setBusy(false); }
  }
  async function logout() {
    setBusy(true); setMessage("");
    try { await send("/api/admin/session/logout", {}); setStatus(false); setAccounts([]); setConnections([]); setMirrors([]); }
    catch { setMessage("Не удалось завершить сеанс."); }
    finally { setBusy(false); }
  }
  async function syncConnection(id) {
    setBusy(true); setMessage("");
    try {
      await send("/api/admin/github/sync", { id });
      setMessage("Синхронизация запланирована; ожидающие изменения также могут быть отправлены.");
      refresh().catch(() => setMessage("Синхронизация запланирована, но состояние источников обновить не удалось."));
    } catch { setMessage("Не удалось запланировать синхронизацию. Проверьте состояние источника и сеанс администратора."); }
    finally { setBusy(false); }
  }
  async function pauseConnection(connection) {
    const paused = !connection.paused;
    setBusy(true); setMessage("");
    try {
      await send("/api/admin/github/pause", { id: connection.id, paused });
      setMessage(paused ? "Синхронизация приостановлена. Уже начатый сетевой запрос может завершиться." : "Синхронизация возобновлена; ожидающие изменения могут быть отправлены.");
      refresh().catch(() => setMessage("Состояние изменено, но список источников обновить не удалось."));
    } catch { setMessage("Не удалось изменить состояние синхронизации. Проверьте источник и сеанс администратора."); }
    finally { setBusy(false); }
  }
  async function configureConnection(event, connection) {
    event.preventDefault(); const data = new FormData(event.currentTarget);
    setBusy(true); setMessage("");
    try {
      await json("/api/admin/github/connections", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ id: connection.id, account_id: data.get("account"), write: data.has("write"), expected_config_revision: connection.config_revision }) });
      setMessage(connection.paused ? "Настройки сохранены. Источник остаётся на паузе до возобновления." : "Настройки сохранены; синхронизация проверит репозиторий и может отправить ожидающие изменения.");
      refresh().catch(() => setMessage("Настройки сохранены, но список источников обновить не удалось."));
    } catch (e) {
      setMessage(githubErrorMessage(e.code, e.status, "Не удалось изменить настройки. Проверьте аккаунт и разрешение записи."));
      refresh().catch(() => setMessage((previous) => `${previous} Не удалось обновить список.`));
    } finally { setBusy(false); }
  }
  async function configureMirror(source, destination, enabled, revision) {
    setBusy(true); setMessage("");
    try {
      await send("/api/admin/github/mirrors", { source_id: source, destination_id: destination, enabled, expected_revision: revision });
      setMessage(enabled ? "Фоновое согласование включено. Проверьте состояние и конфликты." : "Согласование приостановлено; уже ожидающие отправки изменения сохраняются.");
      await refresh();
    } catch (e) { setMessage(githubErrorMessage(e.code, e.status, "Не удалось изменить согласование. Проверьте приватность и права записи источников.")); }
    finally { setBusy(false); }
  }
  return <div className="github-connect reader-access">
    <p>Видимость определяется репозиторием. Wiki использует <code>veda.yaml</code>, программа — <code>docs/</code>.</p>
    {status === false && <><form onSubmit={login}><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Пароль<input name="password" type="password" autoComplete="current-password" required /></label><button disabled={busy}>Войти</button></form><details><summary>Первый вход — установить пароль</summary><form onSubmit={setup}><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Одноразовый код<input name="token" type="password" autoComplete="off" required /></label><label>Новый пароль<input name="password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><label>Повторите пароль<input name="confirm" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><button disabled={busy}>Установить пароль</button></form></details><details><summary>Вход с ключом администратора</summary><form onSubmit={login}><label>Ключ администратора<input name="key" type="password" autoComplete="off" required /></label><button disabled={busy}>Войти</button></form></details></>}
    {status === true && <>
      <form onSubmit={addAccount}><h3>Аккаунт GitHub</h3><label>Идентификатор<input name="id" required placeholder="personal" /></label><label>Способ подключения<select value={accountKind} onChange={(e) => setAccountKind(e.target.value)}><option value="app">GitHub App</option><option value="token">Токен</option></select></label>{accountKind === "app" ? <><label>App ID<input name="app_id" type="number" min="1" step="1" required /></label><label>Installation ID<input name="installation_id" type="number" min="1" step="1" required /></label><label>Приватный ключ App<textarea name="private_key" autoComplete="off" required rows="4" /></label></> : <label>Токен<input name="token" type="password" autoComplete="off" required /></label>}<button disabled={busy}>Сохранить аккаунт</button></form>
      <form onSubmit={connect}><h3>Подключить репозиторий</h3><label>Идентификатор источника<input name="id" required placeholder="handbook" /></label><label>Адрес или owner/repository<input name="repository" required placeholder="owner/repository" /></label><label>Ветка (необязательно)<input name="branch" /></label><label>Аккаунт<select name="account" defaultValue=""><option value="">Без аккаунта — только публичное чтение</option>{accounts.map((a) => <option key={a.id} value={a.id}>{a.login} ({a.id})</option>)}</select></label><label className="github-write"><input type="checkbox" name="write" /> Разрешить запись в репозиторий</label><label className="github-write"><input type="checkbox" name="adopt_existing" /> Привязать существующий приватный источник</label><p>Укажите его идентификатор и аккаунт. Различия станут конфликтами; локальные статьи сохранятся без отправки. После привязки прежний локальный импорт нужно перенастроить.</p><button disabled={busy}>Подключить</button></form>
      <h3>Источники</h3>{connections.length ? <ul>{connections.map((c) => {
        const limited = c.last_error === "rate_limited" && Date.parse(c.next_attempt) > Date.now();
        const detail = limited ? `Лимит GitHub, повтор после ${new Date(c.next_attempt).toLocaleString()}` : c.last_error ? githubErrorMessage(c.last_error, 0, "Проверьте состояние источника.") : "";
        return <li key={c.id}>{c.id}: {c.repository} · {githubVisibilityLabel(c)} · {stateLabels[c.state] || c.state}{detail ? ` (${detail})` : ""} <button type="button" disabled={busy || c.paused || limited} onClick={() => syncConnection(c.id)} aria-label={`Синхронизировать ${c.id}`}>Синхронизировать</button> <button type="button" disabled={busy} onClick={() => pauseConnection(c)} aria-label={`${c.paused ? "Возобновить" : "Приостановить"} синхронизацию ${c.id}`}>{c.paused ? "Возобновить" : "Приостановить"}</button><details><summary>Настроить подключение {c.id}</summary><form key={c.config_revision} onSubmit={(e) => configureConnection(e, c)}><label>Аккаунт<select name="account" defaultValue={c.account_id || ""}><option value="">Без аккаунта — только публичное чтение</option>{accounts.map((a) => <option key={a.id} value={a.id}>{a.login} ({a.id})</option>)}</select></label><label className="github-write"><input type="checkbox" name="write" defaultChecked={c.write} /> Разрешить запись в репозиторий</label><button disabled={busy}>Сохранить настройки</button></form></details></li>;
      })}</ul> : <p>Репозитории ещё не подключены.</p>}
      <h3>Фоновое согласование</h3>
      <p>Приватные источники остаются раздельными. Подключённый источник должен быть полностью синхронизирован и проверен. Расхождения требуют проверки; удаление не выполняется. Для включения нужна совместимая резервная копия: формат хранилища обновится.</p>
      <form onSubmit={(e) => { e.preventDefault(); const d = new FormData(e.currentTarget); configureMirror(d.get("source"), d.get("destination"), true, 0); }}>
        <label>Исходный источник<input name="source" required placeholder="infra" /></label>
        <label>Репозиторий<select name="destination" required defaultValue=""><option value="">Выберите источник GitHub</option>{connections.filter((c) => c.write && c.private && !mirrors.some((m) => m.destination_id === c.id)).map((c) => <option key={c.id} value={c.id}>{c.id} · {c.repository}</option>)}</select></label>
        <button disabled={busy}>Включить согласование</button>
      </form>
      {mirrors.length > 0 && <ul>{mirrors.map((m) => <li key={m.destination_id}>{m.source_id} → {m.destination_id} · {({pending:"Ожидает",ready:"Проход завершён",syncing:"Синхронизация",blocked:"Требует внимания",error:"Ошибка",paused:"Приостановлено"})[m.state] || "Состояние неизвестно"}{m.blocked ? ` · ожидают или требуют проверки: ${m.blocked}` : ""} <button type="button" disabled={busy} onClick={() => configureMirror(m.source_id, m.destination_id, !m.enabled, m.revision)}>{m.enabled ? "Приостановить" : "Возобновить"}</button>{m.blocked_paths?.length > 0 && <details><summary>Показать статьи</summary><ul>{m.blocked_paths.map((p) => <li key={p}>{p}</li>)}</ul>{m.blocked > m.blocked_paths.length && <p>Показаны первые {m.blocked_paths.length} из {m.blocked}.</p>}</details>}</li>)}</ul>}
      <p>Синхронизация также отправляет ожидающие изменения, если запись в репозиторий разрешена. Во время проверки и на паузе публичные статьи источника временно скрыты; локальная копия остаётся доступной авторизованным читателям.</p>
      {sharedSession ? <p>Вход выполнен через учётную запись. Для выхода используйте профиль.</p> : <button onClick={logout} disabled={busy}>Выйти из управления</button>}
    </>}
    {message && <p role="status">{message}</p>}
  </div>;
}
export function App() {
  const [data, setData] = useState(null),
    [error, setError] = useState(""),
    [feature, setFeature] = useState(null);
  const modal = useRef(null);
  useEffect(() => {
    const c = new AbortController();
    let timer;
    async function refreshState() {
      try {
        const r = await fetch("/api/state" + window.location.search, { signal: c.signal });
        if (!r.ok) {
          if (r.status === 404) setData(null);
          throw Error(r.status === 404 ? "Страница не найдена" : "Wiki временно недоступна");
        }
        const next = await r.json();
        if (!c.signal.aborted) { setData(next); setError(""); }
      } catch (e) {
        if (!c.signal.aborted) setError(e.message);
      } finally {
        if (!c.signal.aborted) timer = setTimeout(refreshState, 10000);
      }
    }
    refreshState();
    return () => { c.abort(); clearTimeout(timer); };
  }, [feature]);
  useEffect(() => {
    if (feature) modal.current.showModal();
    else modal.current?.close();
  }, [feature]);
  useEffect(() => {
    if (!data?.view.Page || !window.location.hash) return;
    let id;
    try {
      id = decodeURIComponent(window.location.hash.slice(1));
    } catch {
      return;
    }
    document.getElementById(id)?.scrollIntoView({ block: "start" });
  }, [data?.view.Page?.path]);
  useEffect(() => {
    const listener = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        document.getElementById("search")?.focus();
      }
    };
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, []);
  if (!data)
    return (
      <div className="loading-screen">
        <img src="/assets/veda-logo.png" alt="" />
        <h1>VEDA</h1>
        <p>{error || "Загружаем базу знаний…"}</p>
        {error && <a href="/">Вернуться на главную</a>}
      </div>
    );
  const v = data.view;
  return (
    <Features.Provider value={setFeature}>
      <Sidebar v={v} />
      <main>
        <Header v={v} />
        <SourceWarnings view={v} />
        {v.Page ? (
          <Reading data={data} />
        ) : v.Query || v.Selected ? (
          <Results v={v} />
        ) : (
          <Dashboard v={v} />
        )}
      </main>
      <dialog
        ref={modal}
        onCancel={() => setFeature(null)}
        onClick={(e) => {
          if (e.target === modal.current) setFeature(null);
        }}
      >
        <div className="dialog-head">
          <h2>{feature === "profile" && v.Public ? "Вход" : descriptions[feature]?.[0] || "VEDA"}</h2>
          <button aria-label="Закрыть" onClick={() => setFeature(null)}>
            <I name="x" />
          </button>
        </div>
        {feature === "profile" ? <ReaderAccess /> : feature === "github" ? <GitHubConnect /> : feature === "settings" ? <AccessSettings /> : <p>{descriptions[feature]?.[1]}</p>}
      </dialog>
    </Features.Provider>
  );
}
