import { useState, useEffect, useRef, createContext, useContext } from "react";
import { buildTree, buildMergedTree } from "./tree.js";
import { catalogPages } from "./catalog.js";
import { readVisits, recordVisit, saveVisits, clearVisits, visibleVisits } from "./recent.js";
import { githubErrorMessage, githubVisibilityLabel } from "./githubErrors.js";
import { githubSourceId } from "./githubSourceId.js";
import { conflictWarning, observationLabel, syncWarning } from "./sourceWarnings.js";
import { fetchState } from "./statePoll.js";
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
    ["notes", "Все страницы", "/?env=all"],
    ["affiliate", "Граф знаний", "/?graph=1"],
    ["star", "Избранное", null, "favorites"],
    ["clock", "Недавние", "/?recent=1"],
    ["brand-github", "GitHub", "/?github=1", "github"],
    ["api", "API и MCP", null, "api"],
    ["settings", "Настройки", "/?settings=1", "settings"],
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
                ((href === "/" && !v.Query && !v.Selected && !v.Page && !v.GraphView && !v.RecentView && !v.GitHubView && !v.SettingsView) || (href === "/?env=all" && !!v.Selected && !v.Query && !v.Page) || (href === "/?graph=1" && v.GraphView) || (href === "/?recent=1" && v.RecentView) || (href === "/?github=1" && v.GitHubView) || (href === "/?settings=1" && v.SettingsView)
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
        <h1>{v.SettingsView ? "Настройки" : v.GitHubView ? "Интеграции GitHub" : v.RecentView ? "Недавние" : v.GraphView ? "Граф знаний" : v.Selected && !v.Query && !v.Page ? "Все статьи" : "Вики VEDA"}</h1>
        <p>{v.SettingsView ? "Управление пользователями, командами и ключами доступа" : v.GitHubView ? "Подключение репозиториев и синхронизация с базой знаний" : v.RecentView ? "Страницы, которые вы недавно открывали" : v.GraphView ? "Связи между статьями базы знаний по источникам" : v.Selected && !v.Query && !v.Page ? "Полный каталог статей базы знаний" : "База знаний для работы, проектов и документации"}</p>
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
  const groups = v.Groups || [];
  const covers = ["collection-dunes.jpg", "collection-leaf.jpg", "collection-mountains.jpg"];
  const pageCountLabel = (count) => `${count} ${new Intl.PluralRules("ru").select(count) === "one" ? "страница" : new Intl.PluralRules("ru").select(count) === "few" ? "страницы" : "страниц"}`;
  return (
    <div className="dashboard home-dashboard">
      <div className="home-content">
        {groups.length > 0 && <section className="collections" aria-labelledby="collections-title">
          <h2 id="collections-title">Коллекции знаний</h2>
          <p>Быстрый доступ к разделам базы знаний</p>
          <div className="collection-grid">{groups.slice(0, 3).map((group, index) => <a className="collection" href={'/?env=' + encodeURIComponent(group.ID)} key={group.ID}>
            <img src={'/assets/' + covers[index]} alt="" />
            <span className="collection-copy"><I name="folder" /><span><strong>{group.Name}</strong><small>{group.Description || pageCountLabel(group.Count)}</small></span><I name="arrow-right" /></span>
          </a>)}</div>
        </section>}
        <section className="home-recent" aria-labelledby="home-recent-title">
          <div className="home-section-heading"><div><h2 id="home-recent-title">Недавние страницы</h2><p>Недавно обновлённые материалы</p></div>{v.Count > 0 && <a href="/?env=all">Все страницы <I name="arrow-right" /></a>}</div>
          {v.Count === 0 ? <p className="home-empty">Здесь появятся доступные статьи базы знаний.</p> : <div className="home-page-list">{(v.Cards || []).map((card) => <a className="home-page-row" href={pageHref(card.Page.path)} key={card.Page.path}>
            <span className="home-page-icon"><I name="notes" /></span><strong title={card.Page.title}>{card.Page.title}</strong><span className="home-page-path">{card.Page.path}</span><time dateTime={card.Page.updated_at || undefined}>{updatedLabel(card.Page.updated_at)}</time>
          </a>)}</div>}
        </section>
      </div>
      <aside className="home-tree">
        <Tree groups={groups} />
      </aside>
    </div>
  );
}
function Graph({ v }) {
  const [selected, setSelected] = useState(null);
  const [scale, setScale] = useState(1);
  const [sourceID, setSourceID] = useState("");
  const [pageIndex, setPageIndex] = useState(0);
  const available = v.Groups || [];
  const groups = sourceID ? available.filter((group) => group.ID === sourceID) : available.slice(0, 6);
  const pageCount = Math.max(1, ...groups.map((group) => Math.ceil((group.Pages || []).length / 12)));
  const currentPage = Math.min(pageIndex, pageCount - 1);
  const position = (index) => ({ x: groups.length === 3 && index === 2 ? 255 : (index % 2) * 480 + 15, y: Math.floor(index / 2) * 285 + 8 });
  const nodes = groups.flatMap((group, groupIndex) => {
    const pages = (group.Pages || []).slice(currentPage * 12, currentPage * 12 + 12);
    const origin = position(groupIndex);
    const spaced = pages.length <= 3;
    const points = pages.length === 1 ? [[145, 110]] : pages.length === 2 ? [[55, 110], [240, 110]] : [[145, 65], [35, 150], [255, 150]];
    return pages.map((page, index) => ({ ...page, group: group.Name, groupIndex, width: spaced ? 164 : 132, x: origin.x + (spaced ? points[index][0] : 30 + (index % 3) * 145), y: origin.y + (spaced ? points[index][1] : 62 + Math.floor(index / 3) * 48) }));
  });
  const byPath = new Map(nodes.map((node) => [node.path, node]));
  const edges = (v.GraphEdges || []).filter((edge) => byPath.has(edge.from) && byPath.has(edge.to));
  const chosen = byPath.get(selected);
  const allPages = new Map(available.flatMap((group) => group.Pages || []).map((page) => [page.path, page]));
  const related = chosen ? (v.GraphEdges || []).filter((edge) => edge.from === chosen.path || edge.to === chosen.path).map((edge) => ({ path: edge.from === chosen.path ? edge.to : edge.from, incoming: edge.to === chosen.path })).filter((item) => allPages.has(item.path)) : [];
  const height = Math.max(310, Math.ceil(groups.length / 2) * 285);
  return <section className="graph-section" aria-label="Граф связей статей">
    <div className="graph-filter"><label>Источник <select value={sourceID} onChange={(event) => { setSourceID(event.target.value); setPageIndex(0); setSelected(null); }}><option value="">Все источники</option>{available.map((group) => <option key={group.ID} value={group.ID}>{group.Name}</option>)}</select></label>{pageCount > 1 && <div><button disabled={currentPage === 0} onClick={() => { setPageIndex(currentPage - 1); setSelected(null); }}>Назад</button><span>Страница {currentPage + 1} из {pageCount}</span><button disabled={currentPage + 1 === pageCount} onClick={() => { setPageIndex(currentPage + 1); setSelected(null); }}>Вперёд</button></div>}</div>
    {(v.Count > nodes.length) && <p className="graph-note">Показано {nodes.length} из {v.Count} доступных статей. Выберите источник и страницу, чтобы увидеть остальные; линии показаны только между видимыми узлами.</p>}
    {!nodes.length ? <p className="graph-note">Нет доступных статей для отображения.</p> : <>
      <div className={'graph-canvas-shell' + (groups.length === 3 && scale === 1 ? ' graph-three-groups' : '')}><div className="graph-viewport" tabIndex={0} aria-label="Область графа">
        <div className="graph-stage" style={{ width: 960 * scale, height: height * scale }}>
          <div className="graph-inner" style={{ width: 960, height, transform: `scale(${scale})` }}>
            {groups.map((group, i) => <div className={'graph-cluster graph-cluster-' + (i % 3)} key={group.ID} style={{ left: position(i).x, top: position(i).y }}><strong>{group.Name}</strong></div>)}
            <svg className="graph-lines" width="960" height={height} aria-hidden="true">{edges.map((edge) => { const from = byPath.get(edge.from), to = byPath.get(edge.to); return <line key={edge.from + '\0' + edge.to} x1={from.x + from.width / 2} y1={from.y + 22} x2={to.x + to.width / 2} y2={to.y + 22} />; })}</svg>
            {nodes.map((node) => <button key={node.path} title={node.title} className={'graph-node graph-node-' + (node.groupIndex % 3) + (selected === node.path ? ' selected' : '')} style={{ left: node.x, top: node.y, width: node.width }} onClick={() => setSelected(node.path)}><I name="file-text" /><span>{node.title}</span></button>)}
          </div>
        </div>
      </div>
      <div className="graph-tools"><div className="graph-legend"><strong>Легенда</strong><span><I name="file-text" />Статья</span><span><I name="folder" />Источник</span><span><I name="affiliate" />Связь между статьями</span></div><div className="graph-zoom"><button aria-label="Уменьшить" onClick={() => setScale((s) => Math.max(.6, +(s - .2).toFixed(1)))}>−</button><span>{Math.round(scale * 100)}%</span><button aria-label="Увеличить" onClick={() => setScale((s) => Math.min(1.6, +(s + .2).toFixed(1)))}>+</button></div></div></div>
      <div className="graph-detail">{chosen ? <><I name="file-text" /><span><strong>{chosen.title}</strong><small>{chosen.group} / {chosen.path}</small></span><a href={pageHref(chosen.path)}>Открыть статью <I name="arrow-right" /></a></> : <p>Выберите статью на графе, чтобы открыть её.</p>}</div>
      {chosen && <section className="graph-related" aria-label="Связанные статьи"><h2>Связанные статьи</h2>{related.length ? <ul>{related.map((item) => <li key={(item.incoming ? 'in:' : 'out:') + item.path}><span>{item.incoming ? 'Ссылается на эту статью' : 'Ссылка из этой статьи'}:</span> <a href={pageHref(item.path)}>{allPages.get(item.path).title}</a></li>)}</ul> : <p>Связей с доступными статьями не найдено.</p>}</section>}
    </>}
  </section>;
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
function Recent({ v }) {
  const [entries, setEntries] = useState(() => { try { return readVisits(window.sessionStorage); } catch { return []; } });
  const pages = visibleVisits(entries, v.Groups || []);
  function clear() { try { clearVisits(window.sessionStorage); } catch { /* Storage may be unavailable. */ } setEntries([]); }
  return <section className="recent-history panel" aria-labelledby="recent-history-title">
    <div className="recent-history-heading"><div><h2 id="recent-history-title">История просмотров</h2><p>История хранится в этой вкладке и очищается при выходе.</p></div>{pages.length > 0 && <button onClick={clear}>Очистить историю</button>}</div>
    {pages.length ? <div className="recent-history-list">{pages.map((page) => <a className="recent-history-row" key={page.path} href={pageHref(page.path)}><span className="home-page-icon"><I name="file-text" /></span><span className="recent-history-title"><strong>{page.title}</strong><small>{page.sourceName} / {page.path}</small></span><time dateTime={new Date(page.visitedAt).toISOString()}>{new Intl.DateTimeFormat('ru-RU', { dateStyle: 'medium', timeStyle: 'short' }).format(page.visitedAt)}</time><I name="arrow-right" /></a>)}</div> : <div className="recent-history-empty"><I name="clock" /><h3>Здесь пока пусто</h3><p>Откройте статью, и она появится в истории этой вкладки.</p><a href="/?env=all">Перейти к статьям <I name="arrow-right" /></a></div>}
  </section>;
}
function Results({ v }) {
  const [selectedFolder, setSelectedFolder] = useState(() => {
    const group = v.Selected && v.Selected !== "all" && (v.Groups || []).find((item) => item.ID === v.Selected);
    return group ? { sourceID: group.ID, prefix: group.ID + "/", label: group.Name } : null;
  });
  if (!v.Query) {
    const groups = v.Groups || [];
    const selection = selectedFolder && groups.find((group) => group.ID === selectedFolder.sourceID) ? selectedFolder : null;
    const pages = catalogPages(groups, groups.flatMap((group) => group.Pages || []), selection);
    return <div className="catalog">
      <aside className="catalog-tree panel" aria-label="Источники и папки">
        <h2>Источники и папки</h2>
        <button className={!selection ? "catalog-folder active" : "catalog-folder"} onClick={() => setSelectedFolder(null)}><I name="notes" />Все статьи</button>
        {groups.map((group) => <details className="catalog-branch" key={group.ID} open>
          <summary><I name="chevron-right" /><I name="folder" /><span>{group.Name}</span></summary>
          <button className={selection?.prefix === group.ID + "/" ? "catalog-folder active" : "catalog-folder"} onClick={() => setSelectedFolder({ sourceID: group.ID, prefix: group.ID + "/", label: group.Name })}>Все статьи источника</button>
          <CatalogFolders node={buildTree(group.ID, group.Pages || [])} sourceID={group.ID} prefix={group.ID + "/"} selection={selection} onSelect={setSelectedFolder} parents={[group.Name]} />
        </details>)}
        {!groups.length && <p className="catalog-empty">Доступных разделов пока нет.</p>}
      </aside>
      <section className="catalog-content panel">
        <div className="catalog-breadcrumb"><I name="folder" />{selection ? selection.label : "База знаний"}</div>
        <h2>{selection ? selection.label.split(" / ").at(-1) : "Все статьи"}</h2>
        <div className="catalog-table-head"><span>Название статьи</span><span>Путь в базе знаний</span><span>Обновлено</span></div>
        <div className="catalog-list">{pages.map((page) => <a className="catalog-row" href={pageHref(page.path)} key={page.path}>
          <span className="catalog-title"><span className="home-page-icon"><I name="notes" /></span><strong>{page.title}</strong></span>
          <span className="catalog-path" title={page.path}>{page.path}</span>
          <time dateTime={page.updated_at || undefined}>{updatedLabel(page.updated_at)}</time>
        </a>)}</div>
        {!pages.length && <p className="catalog-empty">В этом разделе пока нет доступных статей.</p>}
      </section>
    </div>;
  }
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
function CatalogFolders({ node, sourceID, prefix, selection, onSelect, parents = [] }) {
  return [...node.folders.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([name, child]) => {
    const folderPrefix = prefix + name + "/";
    const label = [...parents, name];
    return <details className="catalog-branch catalog-subfolder" key={folderPrefix}>
      <summary><I name="chevron-right" /><button className={selection?.prefix === folderPrefix ? "catalog-folder active" : "catalog-folder"} onClick={(event) => { event.preventDefault(); onSelect({ sourceID, prefix: folderPrefix, label: label.join(" / ") }); event.currentTarget.closest("details").open = true; }}><I name="folder" /><span>{name}</span></button></summary>
      <CatalogFolders node={child} sourceID={sourceID} prefix={folderPrefix} selection={selection} onSelect={onSelect} parents={label} />
    </details>;
  });
}
function ReaderAccess() {
  const [status, setStatus] = useState(null);
  const [message, setMessage] = useState("");
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
    const credentials = { username: data.get("username"), password: data.get("password") };
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
      try { clearVisits(window.sessionStorage); } catch { /* Storage may be unavailable. */ }
      window.location.assign("/");
    } catch { setMessage("Не удалось завершить сеанс."); }
  }
  return (
    <div className="reader-access">
      {status === true ? <><p>Приватное чтение включено. Сеанс действует до 8 часов.</p>{canChangePassword && <form onSubmit={changePassword}><h3>Изменить пароль</h3><label>Текущий пароль<input name="current_password" type="password" autoComplete="current-password" required /></label><label>Новый пароль<input name="new_password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><label>Повторите новый пароль<input name="confirm_password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><button>Изменить пароль</button></form>}<button onClick={logout}>Выйти</button></> : status === false ?
        <form onSubmit={submit}><p>Войдите в свою учётную запись.</p><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Пароль<input name="password" type="password" autoComplete="current-password" required /></label><button type="submit">Войти</button></form> : null}
      {message && <p role="alert">{message}</p>}
    </div>
  );
}
function AccessSettings() {
  const [status, setStatus] = useState(null);
  const [sharedSession, setSharedSession] = useState(false);
  const [accessTab, setAccessTab] = useState("users");
  const [accessModal, setAccessModal] = useState(null);
  const accessModalRef = useRef(null);
  useEffect(() => { if (accessModal) accessModalRef.current?.showModal(); else accessModalRef.current?.close(); }, [accessModal]);
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
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setMessage("");
    try { await send("/api/access/session/login", { username: data.get("username"), password: data.get("password") }); setStatus(true); await refresh(); }
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
      setIssued(result); form.reset(); setAccessModal(null);
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
      setEditing(null); setAccessModal(null);
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
    try { await send("/api/access/teams", body); setEditingTeam(null); if (!team) form.reset(); setAccessModal(null); await refresh(); }
    catch (error) { setMessage(error.status === 409 ? "Команда изменилась. Обновите список и проверьте права." : "Не удалось подтвердить изменение команды. Обновите список перед повтором."); }
    finally { setBusy(false); }
  }
  async function createUser(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setMessage("");
    try { await send("/api/access/users", { id: data.get("id"), password: data.get("password"), teams: data.getAll("teams") }); form.reset(); setAccessModal(null); await refresh(); }
    catch { form.reset(); setMessage("Создание не подтверждено. Проверьте список пользователей перед повтором."); }
    finally { setBusy(false); }
  }
  async function updateUser(user, changes) {
    setBusy(true); setMessage("");
    try { await send("/api/access/users", { id: user.id, expected_revision: user.revision, ...changes }); setEditingUser(null); setResettingUser(null); setAccessModal(null); await refresh(); }
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
  const openModal = (type, item = null) => { setMessage(""); setAccessModal({ type, item }); };
  const closeModal = () => { setAccessModal(null); setEditing(null); setEditingTeam(null); setEditingUser(null); setResettingUser(null); };
  const modalTitle = { user: "Новый пользователь", team: "Новая команда", key: "Новый ключ чтения", userTeams: "Изменить команды", password: "Сменить пароль", teamSources: "Источники команды", keySources: "Доступ ключа" };
  return <div className="settings-page reader-access">
    {status === false && <div className="panel settings-login"><p>Для управления доступом войдите как администратор.</p><form onSubmit={login}><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Пароль<input name="password" type="password" autoComplete="current-password" required /></label><button disabled={busy}>Войти в управление доступом</button></form></div>}
    {status === true && <>
      <div className="settings-tabs" aria-label="Разделы настроек">{[["users", "Пользователи", "user"], ["teams", "Команды", "users-group"], ["keys", "Ключи доступа", "key"]].map(([id,label,icon]) => <button type="button" aria-pressed={accessTab === id} key={id} onClick={() => setAccessTab(id)}><I name={icon} />{label}</button>)}</div>
      <section className={`settings-section settings-${accessTab}`}>
        <div className="settings-heading"><div><h2>{accessTab === "users" ? "Пользователи" : accessTab === "teams" ? "Команды" : "Ключи чтения"}</h2><p>{accessTab === "users" ? "Учётные записи и членство в командах" : accessTab === "teams" ? "Группы пользователей и доступные источники" : "Ключи для чтения выбранных источников"}</p></div><button className="settings-add" onClick={() => openModal(accessTab === "users" ? "user" : accessTab === "teams" ? "team" : "key")} disabled={busy || (accessTab === "keys" && (!!issued || !sources.length))}><I name="plus" />{accessTab === "users" ? "Добавить пользователя" : accessTab === "teams" ? "Добавить команду" : "Создать ключ"}</button></div>
        {accessTab === "users" && <div className="settings-list">{users.length ? <><div className="settings-table-head" aria-hidden="true"><span>Пользователь</span><span>Роль</span><span>Команды</span><span>Состояние</span><span></span></div>{users.map((user) => <div className="settings-row" key={user.id}><div className="settings-user"><I name="user" /><strong>{user.id}</strong></div><span><span className="settings-role">{user.admin ? "Администратор" : "Читатель"}</span></span><span>{user.teams.join(", ") || "Без команд"}</span><span><span className={`settings-status ${user.disabled ? "is-disabled" : "is-active"}`}><I name={user.disabled ? "circle" : "circle-check"} />{user.disabled ? "Отключён" : "Активен"}</span></span><div className="settings-row-actions"><button disabled={busy} onClick={() => openModal("userTeams", user)}><I name="users-group" />Команды</button><button disabled={busy} onClick={() => openModal("password", user)}><I name="key" />Сменить пароль</button><button disabled={busy} onClick={() => updateUser(user, { disabled: !user.disabled })}><I name={user.disabled ? "circle-check" : "ban"} />{user.disabled ? "Включить" : "Отключить"}</button></div></div>)}</> : <p>Пользователей пока нет.</p>}</div>}
        {accessTab === "teams" && <div className="settings-list">{teams.length ? teams.map((team) => <div className="settings-row" key={team.id}><div><strong>{team.id}</strong><small>Команда</small></div><span>{team.sources.join(", ")}</span><span></span><div className="settings-row-actions"><button disabled={busy} onClick={() => openModal("teamSources", team)}>Изменить источники</button></div></div>) : <p>Команд пока нет.</p>}</div>}
        {accessTab === "keys" && <div className="settings-list">{keys.length ? keys.map((key) => <div className="settings-row" key={key.id}><div><strong>{key.id}</strong><small>Ключ чтения</small></div><span>{key.sources.join(", ")}</span><span>До {new Date(key.expires_at).toLocaleString("ru-RU")}</span><div className="settings-row-actions"><button disabled={busy} onClick={() => openModal("keySources", key)}>Изменить доступ</button><button disabled={busy} onClick={() => revoke(key.id)}>Отозвать</button></div></div>) : <p>Ключей пока нет.</p>}</div>}
      </section>
      {issued && <div className="access-issued" role="status"><strong>Ключ {issued.id} создан</strong><p>Сохраните его сейчас: повторно VEDA его не покажет.</p><textarea readOnly value={issued.key} aria-label="Новый ключ чтения" rows="3" onFocus={(e) => e.target.select()} /><label>Ссылка для входа<textarea readOnly value={`${window.location.origin}/?key=${encodeURIComponent(issued.key)}`} rows="3" onFocus={(e) => e.target.select()} /></label><p>Ссылка содержит ключ доступа. Передавайте её только доверенному получателю.</p><button type="button" onClick={() => setIssued(null)}>Скрыть ключ</button></div>}
      <dialog className="settings-modal" ref={accessModalRef} onClose={closeModal} onCancel={closeModal} onClick={(event) => { if (event.target === accessModalRef.current) accessModalRef.current.close(); }}><div className="dialog-head"><h2>{modalTitle[accessModal?.type] || "Настройки"}</h2><button type="button" aria-label="Закрыть" onClick={() => accessModalRef.current.close()}><I name="x" /></button></div>
        {accessModal?.type === "user" && <form onSubmit={createUser}><label>Имя пользователя<input name="id" required placeholder="reader-one" /></label><label>Начальный пароль<input name="password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><p>Передайте начальный пароль безопасно; пользователь сможет его сменить.</p><fieldset><legend>Команды пользователя</legend>{teams.map((team) => <label className="access-source" key={team.id}><input type="checkbox" name="teams" value={team.id} /> {team.id}</label>)}</fieldset><button disabled={busy}>Создать пользователя</button></form>}
        {accessModal?.type === "team" && <form onSubmit={saveTeam}><label>Идентификатор<input name="id" required placeholder="team-ops" /></label><fieldset><legend>Источники команды</legend>{sources.map((source) => <label className="access-source" key={source.id}><input type="checkbox" name="sources" value={source.id} /> {source.title} ({source.id})</label>)}</fieldset><button disabled={busy || !sources.length}>Создать команду</button></form>}
        {accessModal?.type === "key" && <form onSubmit={create}><label>Идентификатор<input name="id" required placeholder="agent-one" /></label><label>Срок в днях<input name="days" type="number" min="1" max="365" defaultValue="30" required /></label><fieldset><legend>Доступные источники</legend>{sources.map((source) => <label className="access-source" key={source.id}><input type="checkbox" name="sources" value={source.id} /> {source.title} ({source.id})</label>)}</fieldset><button disabled={busy || !!issued || !sources.length}>Создать ключ</button></form>}
        {accessModal?.type === "userTeams" && <form onSubmit={(event) => updateMembership(event, accessModal.item)}><fieldset><legend>Команды для {accessModal.item.id}</legend>{teams.map((team) => <label className="access-source" key={team.id}><input type="checkbox" name="teams" value={team.id} defaultChecked={accessModal.item.teams.includes(team.id)} /> {team.id}</label>)}</fieldset><button disabled={busy}>Сохранить команды</button></form>}
        {accessModal?.type === "password" && <form onSubmit={(event) => resetPassword(event, accessModal.item)}><label>Новый пароль для {accessModal.item.id}<input name="password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><button disabled={busy}>Сменить пароль</button></form>}
        {accessModal?.type === "teamSources" && <form onSubmit={(event) => saveTeam(event, accessModal.item)}><fieldset><legend>Источники для {accessModal.item.id}</legend>{sources.map((source) => <label className="access-source" key={source.id}><input type="checkbox" name="sources" value={source.id} defaultChecked={accessModal.item.sources.includes(source.id)} /> {source.title} ({source.id})</label>)}</fieldset><button disabled={busy}>Сохранить команду</button></form>}
        {accessModal?.type === "keySources" && <form onSubmit={(event) => update(event, accessModal.item)}><fieldset><legend>Источники для {accessModal.item.id}</legend>{sources.map((source) => <label className="access-source" key={source.id}><input type="checkbox" name="sources" value={source.id} defaultChecked={accessModal.item.sources.includes(source.id)} /> {source.title} ({source.id})</label>)}</fieldset><button disabled={busy}>Сохранить доступ</button></form>}
        {message && <p role="alert">{message}</p>}
      </dialog>
      {sharedSession ? <p>Вход выполнен через учётную запись. Для выхода используйте профиль.</p> : <button type="button" disabled={busy} onClick={logout}>Выйти из управления доступом</button>}
    </>}
    {message && !accessModal && <p role="alert">{message}</p>}
  </div>;
}
function GitHubConnect() {
  const stateLabels = { pending: "Ожидает синхронизации", syncing: "Синхронизация", ready: "Синхронизирован", paused: "Приостановлен", skipped: "Нет папки docs/", blocked: "Требует внимания", error: "Ошибка синхронизации" };
  const [status, setStatus] = useState(null);
  const [sharedSession, setSharedSession] = useState(false);
  const [accounts, setAccounts] = useState([]);
  const [repoAccount, setRepoAccount] = useState(null);
  const [selectedConnection, setSelectedConnection] = useState(null);
  const [githubDialog, setGithubDialog] = useState(null);
  const githubDialogRef = useRef(null);
  useEffect(() => { if (githubDialog) githubDialogRef.current?.showModal(); else githubDialogRef.current?.close(); }, [githubDialog]);
  const [connections, setConnections] = useState([]);
  const [mirrors, setMirrors] = useState([]);
  const [accountKind, setAccountKind] = useState("app");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (repoAccount === null && accounts.length > 0) setRepoAccount(accounts.length === 1 ? accounts[0].id : "");
  }, [accounts, repoAccount]);
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
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setMessage("");
    try { await send("/api/admin/session/login", { username: data.get("username"), password: data.get("password") }); setStatus(true); refresh().catch(() => setMessage("Вход выполнен, но не удалось загрузить настройки.")); }
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
    try { await send("/api/admin/github/accounts", { id: data.get("id"), ...secret }); form.reset(); setGithubDialog(null); setMessage("Аккаунт подключён."); refresh().catch(() => setMessage("Аккаунт подключён, но не удалось обновить список.")); }
    catch (e) { setMessage(githubErrorMessage(e.code, e.status, "Не удалось подключить аккаунт. Проверьте данные и идентификатор.")); }
    finally { if (form.elements.token) form.elements.token.value = ""; if (form.elements.private_key) form.elements.private_key.value = ""; setBusy(false); }
  }
  async function connect(event) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    const account = data.get("account");
    const repository = String(data.get("repository") || "").trim();
    const generatedId = githubSourceId(repository);
    if (!generatedId) { setMessage("Укажите адрес GitHub в виде owner/repository или https://github.com/owner/repository."); return; }
    const customId = String(data.get("id") || "").trim();
    const sourceId = customId || generatedId;
    const adopted = data.has("adopt_existing");
    if (!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(sourceId) || (adopted && !customId)) { setMessage("Укажите корректный идентификатор: строчные латинские буквы, цифры, дефис или подчёркивание. Для привязки существующего источника нужен его идентификатор."); return; }
    setBusy(true); setMessage("Проверяем доступ к репозиторию и его ветке…");
    try {
      await send("/api/admin/github/connections", { id: sourceId, repository, account_id: account, branch: data.get("branch"), write: data.has("write"), adopt_existing: adopted });
      form.reset(); setGithubDialog(null); setMessage(adopted ? "Источник привязан. Проверьте конфликты и отключите прежний локальный импорт." : `Репозиторий подключён как ${sourceId}; импорт выполняется в фоне.`); refresh().catch(() => setMessage("Репозиторий подключён, но не удалось обновить список."));
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
  const selected = connections.find((connection) => connection.id === selectedConnection) || connections[0];
  return <div className="github-connect github-page reader-access">
    <p>Видимость определяется репозиторием. Wiki использует <code>veda.yaml</code>, программа — <code>docs/</code>.</p>
    {status === false && <><form onSubmit={login}><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Пароль<input name="password" type="password" autoComplete="current-password" required /></label><button disabled={busy}>Войти</button></form><details><summary>Первый вход — установить пароль</summary><form onSubmit={setup}><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Одноразовый код<input name="token" type="password" autoComplete="off" required /></label><label>Новый пароль<input name="password" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><label>Повторите пароль<input name="confirm" type="password" minLength="10" maxLength="128" autoComplete="new-password" required /></label><button disabled={busy}>Установить пароль</button></form></details></>}
    {status === true && <>
      <div className="github-page-actions"><button type="button" className="github-action secondary" onClick={() => { setMessage(""); setGithubDialog("account"); }}>Добавить аккаунт</button><button type="button" className="github-action primary" onClick={() => { setMessage(""); setGithubDialog("repository"); }}>Подключить репозиторий</button></div>
      <dialog className="github-action-dialog" ref={githubDialogRef} onClose={() => setGithubDialog(null)} onCancel={() => setGithubDialog(null)} onClick={(event) => { if (event.target === githubDialogRef.current) githubDialogRef.current.close(); }}>
        <div className="dialog-head"><h2>{githubDialog === "account" ? "Аккаунт GitHub" : "Подключить репозиторий"}</h2><button type="button" aria-label="Закрыть" onClick={() => githubDialogRef.current.close()}><I name="x" /></button></div>
        {githubDialog === "account" && <form onSubmit={addAccount}><label>Идентификатор<input name="id" required placeholder="personal" /></label><label>Способ подключения<select value={accountKind} onChange={(e) => setAccountKind(e.target.value)}><option value="app">GitHub App</option><option value="token">Токен</option></select></label>{accountKind === "app" ? <><label>App ID<input name="app_id" type="number" min="1" step="1" required /></label><label>Installation ID<input name="installation_id" type="number" min="1" step="1" required /></label><label>Приватный ключ App<textarea name="private_key" autoComplete="off" required rows="4" /></label></> : <label>Токен<input name="token" type="password" autoComplete="off" required /></label>}<button disabled={busy}>Сохранить аккаунт</button></form>}
        {githubDialog === "repository" && <form onSubmit={connect}><label>Адрес или owner/repository<input name="repository" required placeholder="owner/repository" /></label><label>Ветка (необязательно)<input name="branch" /></label><label>Аккаунт<select name="account" value={repoAccount || ""} onChange={(event) => setRepoAccount(event.target.value)}><option value="">Без аккаунта — только публичное чтение</option>{accounts.map((a) => <option key={a.id} value={a.id}>{a.login} ({a.id})</option>)}</select></label><label className="github-write"><input type="checkbox" name="write" /> Разрешить запись в репозиторий</label><p>Идентификатор источника будет выбран автоматически; импорт и наблюдение начнутся после подключения.</p><details><summary>Дополнительные настройки источника</summary><label>Идентификатор источника<input name="id" placeholder="автоматически" /></label><label className="github-write"><input type="checkbox" name="adopt_existing" /> Привязать существующий приватный источник</label><p>Для привязки укажите его идентификатор и аккаунт. Локальные статьи сохранятся без автоматической отправки; прежний импорт нужно перенастроить.</p></details><button disabled={busy}>{busy ? "Проверяем…" : "Подключить"}</button></form>}
        {message && <p role="status">{message}</p>}
      </dialog>
      <div className="github-workspace"><aside className="github-accounts panel"><h2>Подключённые аккаунты</h2>{accounts.map((account) => <section className="github-account" key={account.id}><strong><I name="brand-github" />{account.login || account.id}</strong><small>{account.id}</small>{connections.filter((connection) => connection.account_id === account.id).map((connection) => <button type="button" className={selected?.id === connection.id ? "github-repo active" : "github-repo"} key={connection.id} onClick={() => setSelectedConnection(connection.id)}><I name="notes" />{connection.repository}</button>)}</section>)}{connections.filter((connection) => !accounts.some((account) => account.id === connection.account_id)).length > 0 && <section className="github-account"><strong>Другие подключения</strong>{connections.filter((connection) => !accounts.some((account) => account.id === connection.account_id)).map((connection) => <button type="button" className={selected?.id === connection.id ? "github-repo active" : "github-repo"} key={connection.id} onClick={() => setSelectedConnection(connection.id)}><I name="notes" />{connection.repository}</button>)}</section>}{!accounts.length && !connections.length && <p>Аккаунты и репозитории ещё не подключены.</p>}</aside>
      <section className="github-repository panel">{selected ? [selected].map((c) => {
        const limited = c.last_error === "rate_limited" && Date.parse(c.next_attempt) > Date.now();
        const detail = limited ? `Лимит GitHub, повтор после ${new Date(c.next_attempt).toLocaleString()}` : c.last_error ? githubErrorMessage(c.last_error, 0, "Проверьте состояние источника.") : "";
        return <div key={c.id}><div className="github-breadcrumb">GitHub / {c.account_id || "Публичный доступ"} / {c.repository}</div><h2>{c.repository}</h2><p>Источник: {c.id}</p><dl className="github-facts"><div><dt>Видимость</dt><dd>{githubVisibilityLabel(c)}</dd></div><div><dt>Аккаунт</dt><dd>{c.account_id || "Без аккаунта"}</dd></div><div><dt>Состояние синхронизации</dt><dd>{stateLabels[c.state] || "Состояние неизвестно"}{detail ? ` — ${detail}` : ""}</dd></div><div><dt>Последняя проверка</dt><dd>{c.verified_at ? new Date(c.verified_at).toLocaleString("ru-RU") : "Нет данных"}</dd></div><div><dt>Право на запись</dt><dd>{c.write ? "Разрешено" : "Только чтение"}</dd></div></dl><div className="github-repo-actions"><button type="button" disabled={busy || c.paused || limited} onClick={() => syncConnection(c.id)} aria-label={`Синхронизировать ${c.id}`}>Синхронизировать</button> <button type="button" disabled={busy} onClick={() => pauseConnection(c)} aria-label={`${c.paused ? "Возобновить" : "Приостановить"} синхронизацию ${c.id}`}>{c.paused ? "Возобновить" : "Приостановить"}</button><details><summary>Настроить подключение {c.id}</summary><form key={c.config_revision} onSubmit={(e) => configureConnection(e, c)}><label>Аккаунт<select name="account" defaultValue={c.account_id || ""}><option value="">Без аккаунта — только публичное чтение</option>{accounts.map((a) => <option key={a.id} value={a.id}>{a.login} ({a.id})</option>)}</select></label><label className="github-write"><input type="checkbox" name="write" defaultChecked={c.write} /> Разрешить запись в репозиторий</label><button disabled={busy}>Сохранить настройки</button></form></details></div></div>;
      }) : <p>Выберите репозиторий или подключите новый.</p>}</section></div>
      <details className="github-mirrors panel"><summary>Фоновое согласование источников</summary><h3>Фоновое согласование</h3>
      <p>Приватные источники остаются раздельными. Подключённый источник должен быть полностью синхронизирован и проверен. Расхождения требуют проверки; удаление не выполняется. Для включения нужна совместимая резервная копия: формат хранилища обновится.</p>
      <form onSubmit={(e) => { e.preventDefault(); const d = new FormData(e.currentTarget); configureMirror(d.get("source"), d.get("destination"), true, 0); }}>
        <label>Исходный источник<input name="source" required placeholder="infra" /></label>
        <label>Репозиторий<select name="destination" required defaultValue=""><option value="">Выберите источник GitHub</option>{connections.filter((c) => c.write && c.private && !mirrors.some((m) => m.destination_id === c.id)).map((c) => <option key={c.id} value={c.id}>{c.id} · {c.repository}</option>)}</select></label>
        <button disabled={busy}>Включить согласование</button>
      </form>
      {mirrors.length > 0 && <ul>{mirrors.map((m) => <li key={m.destination_id}>{m.source_id} → {m.destination_id} · {({pending:"Ожидает",ready:"Проход завершён",syncing:"Синхронизация",blocked:"Требует внимания",error:"Ошибка",paused:"Приостановлено"})[m.state] || "Состояние неизвестно"}{m.blocked ? ` · ожидают или требуют проверки: ${m.blocked}` : ""} <button type="button" disabled={busy} onClick={() => configureMirror(m.source_id, m.destination_id, !m.enabled, m.revision)}>{m.enabled ? "Приостановить" : "Возобновить"}</button>{m.blocked_paths?.length > 0 && <details><summary>Показать статьи</summary><ul>{m.blocked_paths.map((p) => <li key={p}>{p}</li>)}</ul>{m.blocked > m.blocked_paths.length && <p>Показаны первые {m.blocked_paths.length} из {m.blocked}.</p>}</details>}</li>)}</ul>}
      </details><p>Синхронизация также отправляет ожидающие изменения, если запись в репозиторий разрешена. Во время проверки и на паузе публичные статьи источника временно скрыты; локальная копия остаётся доступной авторизованным читателям.</p>
      {sharedSession ? <p>Вход выполнен через учётную запись. Для выхода используйте профиль.</p> : <button onClick={logout} disabled={busy}>Выйти из управления</button>}
    </>}
    {message && <p role="status">{message}</p>}
  </div>;
}
export function App({ linkLoginError = false }) {
  const [data, setData] = useState(null),
    [error, setError] = useState(""),
    [feature, setFeature] = useState(null);
  const modal = useRef(null);
  useEffect(() => {
    const c = new AbortController();
    let timer, etag = "";
    async function refreshState() {
      try {
        const next = await fetchState(fetch, "/api/state" + window.location.search, c.signal, etag);
        if (!c.signal.aborted) {
          etag = next.etag;
          if (!next.unchanged) setData(next.data);
          setError("");
        }
      } catch (e) {
        if (!c.signal.aborted) {
          if (e.status) etag = "";
          if (e.status === 404) setData(null);
          setError(e.message);
        }
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
    if (!data?.view.Page?.path) return;
    try { const storage = window.sessionStorage; saveVisits(storage, recordVisit(readVisits(storage), data.view.Page.path, Date.now())); } catch { /* The article remains readable without storage. */ }
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
        {linkLoginError && <p role="alert">Не удалось войти по ссылке. Проверьте ключ и адрес подключения.</p>}
        {error && <a href="/">Вернуться на главную</a>}
      </div>
    );
  const params = new URLSearchParams(window.location.search);
  const v = { ...data.view, SettingsView: !data.view.Public && params.get("settings") === "1" && !data.view.Query && !data.view.Page, GitHubView: !data.view.Public && params.get("github") === "1" && !data.view.Query && !data.view.Page, GraphView: params.get("graph") === "1" && !data.view.Query && !data.view.Page, RecentView: params.get("recent") === "1" && !data.view.Query && !data.view.Page };
  return (
    <Features.Provider value={setFeature}>
      <Sidebar v={v} />
      <main>
        <Header v={v} />
        {linkLoginError && <p role="alert">Не удалось войти по ссылке. Проверьте ключ и адрес подключения.</p>}
        <SourceWarnings view={v} />
        {v.SettingsView ? <AccessSettings /> : v.GitHubView ? <GitHubConnect /> : v.RecentView ? <Recent v={v} /> : v.GraphView ? <Graph v={v} /> : v.Page ? (
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
        {feature === "profile" ? <ReaderAccess /> : feature === "github" && !v.Public ? <GitHubConnect /> : feature === "settings" && !v.Public ? <AccessSettings /> : <p>{descriptions[feature]?.[1]}</p>}
      </dialog>
    </Features.Provider>
  );
}
