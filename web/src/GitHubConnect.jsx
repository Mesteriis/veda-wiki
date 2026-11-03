import { useState, useEffect, useRef } from "react";
import { I } from "./icon.jsx";
import { githubErrorMessage, githubVisibilityLabel } from "./githubErrors.js";
import { githubSourceId } from "./githubSourceId.js";

export function GitHubConnect() {
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
