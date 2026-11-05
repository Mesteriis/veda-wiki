import { useState, type FormEvent } from "react";
import { createKey, listKeys, listSources, listTeams, listUsers, revokeKey, saveTeam as postTeam, saveUser, updateKey, type UserChanges } from "../../api/access";
import { statusOf } from "../../api/http";
import { login as postLogin, logout as postLogout, type Credentials } from "../../api/session";
import type { AccessKey, IssuedKey as Issued, SourceChoice, WikiTeam, WikiUser } from "../../api/types";
import { I } from "../../components/Icon";
import { useSessionBootstrap } from "../../hooks/useSessionBootstrap";
import { field, fields } from "../../lib/forms";
import { LoginForm } from "../session/LoginForm";
import { AccessModal, type AccessModalState } from "./AccessModal";
import { IssuedKey } from "./IssuedKey";
import { KeysTab } from "./KeysTab";
import { RequestsTab } from "./RequestsTab";
import { TelegramTab } from "./TelegramTab";
import { TeamsTab } from "./TeamsTab";
import { UsersTab } from "./UsersTab";

type Tab = "users" | "teams" | "keys" | "requests" | "telegram";

const tabs: [Tab, string, string][] = [["users", "Пользователи", "user"], ["teams", "Команды", "users-group"], ["keys", "Ключи доступа", "key"], ["requests", "Запросы доступа", "clipboard-check"], ["telegram", "Telegram", "brand-telegram"]];
const headings: Record<Tab, [title: string, text: string, add: string | null]> = {
  users: ["Пользователи", "Учётные записи и членство в командах", "Добавить пользователя"],
  teams: ["Команды", "Группы пользователей и доступные источники", "Добавить команду"],
  keys: ["Ключи чтения", "Ключи для чтения выбранных источников", "Создать ключ"],
  requests: ["Запросы доступа", "Запросы гостей на доступ к API/MCP по публичным разделам", null],
  telegram: ["Telegram", "Уведомления администраторам через бота", null],
};
const newItem: Partial<Record<Tab, "user" | "team" | "key">> = { users: "user", teams: "team", keys: "key" };

// Users, teams and reader keys, managed in an access-administrator session.
export function AccessSettings() {
  const [accessTab, setAccessTab] = useState<Tab>("users");
  const [accessModal, setAccessModal] = useState<AccessModalState>(null);
  const [sources, setSources] = useState<SourceChoice[]>([]);
  const [keys, setKeys] = useState<AccessKey[]>([]);
  const [teams, setTeams] = useState<WikiTeam[]>([]);
  const [users, setUsers] = useState<WikiUser[]>([]);
  const [issued, setIssued] = useState<Issued | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function refresh() {
    const [available, existing, groups, people] = await Promise.all([listSources(), listKeys(), listTeams(), listUsers()]);
    setSources(available); setKeys(existing); setTeams(groups); setUsers(people);
  }
  const { status, setStatus, sharedSession } = useSessionBootstrap("/api/access/session", {
    unavailableMessage: "Управление доступно на приватном адресе через HTTPS или на этом компьютере.",
    onMessage: setMessage,
    onAuthenticated: () => refresh(),
  });
  async function login(credentials: Credentials) {
    setBusy(true); setMessage("");
    try { await postLogin("/api/access/session", credentials); setStatus(true); await refresh(); }
    catch { setMessage("Не удалось войти или загрузить источники. Проверьте подключение."); }
    finally { setBusy(false); }
  }
  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    const days = Number(field(data, "days")); const selected = fields(data, "sources");
    if (!Number.isInteger(days) || days < 1 || days > 365 || !selected.length) { setMessage("Выберите источники и срок от 1 до 365 дней."); return; }
    setBusy(true); setMessage(""); setIssued(null);
    try {
      const result = await createKey(field(data, "id"), selected, new Date(Date.now() + days * 86400000).toISOString());
      setIssued(result); form.reset(); setAccessModal(null);
      try { await refresh(); } catch { setMessage("Ключ создан, но список не обновился. Сохраните ключ и обновите список позже."); }
    } catch { setMessage("Создание не подтверждено: ключ мог быть создан. Проверьте список перед повтором."); }
    finally { setBusy(false); }
  }
  async function revoke(id: string) {
    if (!window.confirm(`Отозвать ключ ${id}? Доступ по нему прекратится.`)) return;
    setBusy(true); setMessage("");
    try { await revokeKey(id); await refresh(); }
    catch { setMessage("Не удалось подтвердить отзыв. Обновите список перед повтором."); }
    finally { setBusy(false); }
  }
  async function update(event: FormEvent<HTMLFormElement>, key: AccessKey) {
    event.preventDefault();
    const selected = fields(new FormData(event.currentTarget), "sources");
    if (!selected.length) { setMessage("Выберите хотя бы один источник."); return; }
    setBusy(true); setMessage("");
    try {
      await updateKey(key, selected);
      setAccessModal(null);
      try { await refresh(); } catch { setMessage("Права изменены, но список не обновился. Обновите его позже."); }
    } catch (error) {
      setMessage(statusOf(error) === 409 ? "Ключ изменился или срок истёк. Обновите список и проверьте права." : "Изменение не подтверждено. Обновите список перед повтором.");
    } finally { setBusy(false); }
  }
  async function saveTeam(event: FormEvent<HTMLFormElement>, team: WikiTeam | null = null) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form); const selected = fields(data, "sources");
    if (!selected.length) { setMessage("Выберите хотя бы один источник для команды."); return; }
    setBusy(true); setMessage("");
    try { await postTeam(team?.id || field(data, "id"), selected, team ? team.revision : undefined); if (!team) form.reset(); setAccessModal(null); await refresh(); }
    catch (error) { setMessage(statusOf(error) === 409 ? "Команда изменилась. Обновите список и проверьте права." : "Не удалось подтвердить изменение команды. Обновите список перед повтором."); }
    finally { setBusy(false); }
  }
  async function createUser(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setMessage("");
    try { await saveUser(field(data, "id"), { password: field(data, "password"), teams: fields(data, "teams") }); form.reset(); setAccessModal(null); await refresh(); }
    catch { form.reset(); setMessage("Создание не подтверждено. Проверьте список пользователей перед повтором."); }
    finally { setBusy(false); }
  }
  async function updateUser(user: WikiUser, changes: UserChanges) {
    setBusy(true); setMessage("");
    try { await saveUser(user.id, changes, user.revision); setAccessModal(null); await refresh(); }
    catch (error) { setMessage(statusOf(error) === 409 ? (changes.disabled ? "Отключение отклонено: пользователь изменился или это последний активный администратор. Обновите список." : "Пользователь изменился. Обновите список и повторите действие.") : "Изменение не подтверждено. Обновите список перед повтором."); }
    finally { setBusy(false); }
  }
  function updateMembership(event: FormEvent<HTMLFormElement>, user: WikiUser) { event.preventDefault(); void updateUser(user, { teams: fields(new FormData(event.currentTarget), "teams") }); }
  function resetPassword(event: FormEvent<HTMLFormElement>, user: WikiUser) { event.preventDefault(); const form = event.currentTarget; const password = field(new FormData(form), "password"); form.reset(); void updateUser(user, { password }); }
  async function logout() {
    setBusy(true); setMessage("");
    try { await postLogout("/api/access/session"); setStatus(false); setKeys([]); setSources([]); setTeams([]); setUsers([]); setIssued(null); }
    catch { setMessage("Не удалось завершить сеанс."); }
    finally { setBusy(false); }
  }
  const openModal = (modal: NonNullable<AccessModalState>) => { setMessage(""); setAccessModal(modal); };
  const [title, text, add] = headings[accessTab];
  return <div className="settings-page reader-access">
    {status === false && <div className="panel settings-login"><p>Для управления доступом войдите как администратор.</p><LoginForm submitLabel="Войти в управление доступом" onLogin={login} busy={busy} /></div>}
    {status === true && <>
      <div className="settings-tabs" aria-label="Разделы настроек">{tabs.map(([id, label, icon]) => <button type="button" aria-pressed={accessTab === id} key={id} onClick={() => setAccessTab(id)}><I name={icon} />{label}</button>)}</div>
      <section className={`settings-section settings-${accessTab}`}>
        <div className="settings-heading"><div><h2>{title}</h2><p>{text}</p></div>{add && <button className="settings-add" onClick={() => { const type = newItem[accessTab]; if (type) openModal({ type }); }} disabled={busy || (accessTab === "keys" && (!!issued || !sources.length))}><I name="plus" />{add}</button>}</div>
        {accessTab === "users" && <UsersTab users={users} busy={busy} onTeams={(item) => openModal({ type: "userTeams", item })} onPassword={(item) => openModal({ type: "password", item })} onToggle={(user) => { void updateUser(user, { disabled: !user.disabled }); }} />}
        {accessTab === "teams" && <TeamsTab teams={teams} busy={busy} onSources={(item) => openModal({ type: "teamSources", item })} />}
        {accessTab === "requests" && <RequestsTab busy={busy} setBusy={setBusy} onIssued={(result) => { setIssued(result); refresh().catch(() => setMessage("Ключ выдан, но список ключей не обновился.")); }} />}
        {accessTab === "telegram" && <TelegramTab busy={busy} setBusy={setBusy} />}
        {accessTab === "keys" && <KeysTab keys={keys} busy={busy} onSources={(item) => openModal({ type: "keySources", item })} onRevoke={(id) => { void revoke(id); }} />}
      </section>
      {issued && <IssuedKey issued={issued} onHide={() => setIssued(null)} />}
      <AccessModal modal={accessModal} sources={sources} teams={teams} busy={busy} keyPending={!!issued} message={message} onClose={() => setAccessModal(null)}
        onCreateUser={(event) => { void createUser(event); }} onCreateTeam={(event) => { void saveTeam(event); }} onCreateKey={(event) => { void create(event); }}
        onUserTeams={updateMembership} onPassword={resetPassword} onTeamSources={(event, team) => { void saveTeam(event, team); }} onKeySources={(event, key) => { void update(event, key); }} />
      {sharedSession ? <p>Вход выполнен через учётную запись. Для выхода используйте профиль.</p> : <button type="button" disabled={busy} onClick={logout}>Выйти из управления доступом</button>}
    </>}
    {message && !accessModal && <p role="alert">{message}</p>}
  </div>;
}
