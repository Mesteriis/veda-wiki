import { useState, useEffect, useRef, createContext, useContext } from "react";
import { buildTree } from "./tree.js";
const Features = createContext(() => {});
const descriptions = {
  github: [
    "GitHub",
    "GitHub подключается через REST API или MCP с явным выбором аккаунта. Публичность определяется видимостью репозитория. Wiki использует veda.yaml; программы используют docs/. Автоматический перевод ещё не подключён. Источники хранятся раздельно и объединяются для чтения.",
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
    "Статьи создаются и обновляются через REST API или MCP. Веб-интерфейс предназначен только для чтения. GitHub настраивается через REST/MCP; статьи хранятся локально.",
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
function Sidebar({ v }) {
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
    <aside className="sidebar">
      <a className="brand" href="/">
        <img src="/assets/veda-logo.png" alt="" className="brand-mark" />
        <span>
          <strong>VEDA</strong>
          <small>KNOWLEDGE BASE</small>
        </span>
      </a>
      <nav aria-label="Основная навигация">
        {nav.map(([icon, label, href, feature]) =>
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
      <div className="sidebar-bottom">
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
                  {active ? (name === "GitHub" ? "Настроен" : "Online") : "Не настроен"}
                </dd>
              </div>
            ))}
          </dl>
        </section>
      </div>
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
        <span className="reading-mode"><I name="shield-check" />Только чтение</span>
        <F className="round" name="notifications" label="Уведомления">
          <I name="bell" />
        </F>
        <F className="round" name="theme" label="Оформление">
          <I name="sun" />
        </F>
        <F className="round profile" name="profile" label="Профиль">
          V
        </F>
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
      title="Страницы wiki"
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
                <span>Из текущего снимка</span>
                <span className="avatar">A</span>
              </div>
            </div>
          </a>
        ))}
      </div>
    </Panel>
  );
}
function Tree({ groups }) {
  return (
    <Panel
      title="Структура знания"
      className="knowledge-tree"
      actions={
        <>
          <F name="settings" label="Настройки структуры">
            <I name="dots" />
          </F>
        </>
      }
    >
      <a className="tree-home" id="knowledge-tree" href="/">
        <I name="chevron-right" />
        <I name="home" />
        Главная
      </a>
      {groups.map((g) => (
        <details className="tree-group" key={g.ID}>
          <summary>
            <I name="chevron-right" />
            <I name="folder" />
            <span>{g.Name}</span>
            <small>{g.Count}</small>
          </summary>
          <div className="tree-pages">
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
          <I name="file-text" /><span>{page.title}</span>
        </a>
      ))}
    </>
  );
}
function Popular({ cards }) {
  return (
    <Panel
      title="Популярные страницы"
      className="popular"
      actions={
        <div className="tabs">
          {["За неделю", "За месяц", "За год"].map((s, i) => (
            <F name="recent" className={i === 0 ? "active" : ""} key={s}>
              {s}
            </F>
          ))}
        </div>
      }
    >
      <div className="popular-list">
        {cards.map((c, i) => (
          <a href={pageHref(c.Page.path)} key={c.Page.path}>
            <span className="rank">{i + 1}</span>
            <span className="tiny-tile">
              <I name="file-description" />
            </span>
            <span className="popular-label">
              <strong>{c.Page.title}</strong>
              <small>/{c.Page.path}</small>
            </span>
            <span className="popularity-track" />
            <span className="unmeasured">—</span>
          </a>
        ))}
      </div>
      <p className="data-note">История просмотров ещё не подключена</p>
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
              : "Не настроен"}
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
            <Popular cards={v.Cards || []} />
            <Activity />
          </div>
        </div>
      </div>
      <aside className="right-column">
        {<Integrations isPublic={v.Public} githubCount={v.GitHubCount || 0} />}
        <Statistics count={v.Count} groups={(v.Groups || []).length} isPublic={v.Public} />
        <Shortcuts />
      </aside>
    </div>
  );
}
function Reading({ data }) {
  const v = data.view;
  return (
    <div className="reading-layout">
      <article className="panel reading">
        <a className="back-link" href="/">
          Главная
        </a>
        <div className="source-path">{v.Page.path}</div>
        <div className="provenance">
          {v.Page.translated ? "Перевод на русский · SHA256" : "SHA256"} <code>{v.Page.sha256}</code>
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
  useEffect(() => {
    const controller = new AbortController();
    fetch("/api/session/status", { signal: controller.signal })
      .then((r) => { if (!r.ok) throw Error(); return r.json(); })
      .then((result) => setStatus(result.authenticated))
      .catch((e) => { if (e.name !== "AbortError") setMessage("Вход доступен на приватном адресе через HTTPS или на этом компьютере."); });
    return () => controller.abort();
  }, []);
  async function submit(event) {
    event.preventDefault();
    const form = event.currentTarget;
    const key = new FormData(form).get("key");
    setMessage("");
    try {
      const response = await fetch("/api/session/login", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ key }) });
      form.reset();
      if (!response.ok) throw Error();
      window.location.reload();
    } catch { setMessage("Не удалось войти. Проверьте ключ и адрес подключения."); }
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
      {status === true ? <><p>Приватное чтение включено. Сеанс действует до 8 часов.</p><button onClick={logout}>Выйти</button></> : status === false ?
        <form onSubmit={submit}><p>Введите ключ чтения, чтобы открыть приватные статьи.</p><label>Ключ чтения<input name="key" type="password" autoComplete="off" required /></label><button type="submit">Войти</button></form> : null}
      {message && <p role="alert">{message}</p>}
    </div>
  );
}
export function App() {
  const [data, setData] = useState(null),
    [error, setError] = useState(""),
    [feature, setFeature] = useState(null);
  const modal = useRef(null);
  useEffect(() => {
    const c = new AbortController();
    fetch("/api/state" + window.location.search, { signal: c.signal })
      .then((r) => {
        if (!r.ok)
          throw Error(
            r.status === 404
              ? "Страница не найдена"
              : "Wiki временно недоступна",
          );
        return r.json();
      })
      .then(setData)
      .catch((e) => {
        if (e.name !== "AbortError") setError(e.message);
      });
    return () => c.abort();
  }, []);
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
  }, [data]);
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
          <h2>{descriptions[feature]?.[0] || "VEDA"}</h2>
          <button aria-label="Закрыть" onClick={() => setFeature(null)}>
            <I name="x" />
          </button>
        </div>
        {feature === "profile" ? <ReaderAccess /> : <p>{descriptions[feature]?.[1]}</p>}
      </dialog>
    </Features.Provider>
  );
}
