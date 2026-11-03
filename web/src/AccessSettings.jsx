import { useState, useEffect, useRef } from "react";
import { I } from "./icon.jsx";

export function AccessSettings() {
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
    catch (error) { setMessage(error.status === 409 ? (changes.disabled ? "Отключение отклонено: пользователь изменился или это последний активный администратор. Обновите список." : "Пользователь изменился. Обновите список и повторите действие.") : "Изменение не подтверждено. Обновите список перед повтором."); }
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
