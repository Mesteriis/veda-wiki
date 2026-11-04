import { useEffect, useState, type FormEvent } from "react";
import { addAccount as postAccount, configureMirror as postMirror, connect as postConnection, listAccounts, listConnections, listMirrors, pause, reconfigure, sync, type AccountSecret } from "../../api/github";
import { HttpError, statusOf } from "../../api/http";
import { login as postLogin, logout as postLogout, setupPassword, type Credentials } from "../../api/session";
import type { GitHubAccount, GitHubConnection, SourceMirror } from "../../api/types";
import { useSessionBootstrap } from "../../hooks/useSessionBootstrap";
import { field } from "../../lib/forms";
import { githubErrorMessage } from "../../lib/githubErrors";
import { githubSourceId } from "../../lib/githubSourceId";
import { LoginForm } from "../session/LoginForm";
import { Accounts } from "./Accounts";
import { Connections } from "./Connections";
import { GitHubDialog, type AccountKind, type GitHubDialogKind } from "./GitHubDialog";
import { Mirrors } from "./Mirrors";

// Only a known server code is shown; a network failure has neither code nor status.
function failure(error: unknown, fallback: string): string {
  return githubErrorMessage(error instanceof HttpError ? error.code : undefined, statusOf(error), fallback);
}

// Secrets must not stay in the form once the request has finished.
function clearField(form: HTMLFormElement, name: string) {
  const element = form.elements.namedItem(name);
  if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement) element.value = "";
}

// GitHub accounts, repository connections and mirrors, managed in an administrator session.
export function GitHubConnect() {
  const [accounts, setAccounts] = useState<GitHubAccount[]>([]);
  const [repoAccount, setRepoAccount] = useState<string | null>(null);
  const [selectedConnection, setSelectedConnection] = useState<string | null>(null);
  const [githubDialog, setGithubDialog] = useState<GitHubDialogKind>(null);
  const [connections, setConnections] = useState<GitHubConnection[]>([]);
  const [mirrors, setMirrors] = useState<SourceMirror[]>([]);
  const [accountKind, setAccountKind] = useState<AccountKind>("app");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function refresh() {
    const [a, c, m] = await Promise.all([listAccounts(), listConnections(), listMirrors()]);
    setAccounts(a); setConnections(c); setMirrors(m);
    // The first loaded list picks the repository account once: the only one, or none.
    setRepoAccount((current) => current === null && a.length > 0 ? (a.length === 1 ? a[0]?.id ?? "" : "") : current);
  }
  const { status, setStatus, sharedSession } = useSessionBootstrap("/api/admin/session", {
    unavailableMessage: "Управление доступно на приватном адресе через HTTPS или на этом компьютере.",
    onMessage: setMessage,
    onAuthenticated: () => refresh(),
  });
  useEffect(() => {
    if (!status) return;
    const timer = setInterval(() => { refresh().catch(() => setMessage("Не удалось обновить состояние GitHub.")); }, 10000);
    return () => clearInterval(timer);
  }, [status]);
  async function login(credentials: Credentials) {
    setBusy(true); setMessage("");
    try { await postLogin("/api/admin/session", credentials); setStatus(true); refresh().catch(() => setMessage("Вход выполнен, но не удалось загрузить настройки.")); }
    catch { setMessage("Не удалось войти. Проверьте данные и подключение."); }
    finally { setBusy(false); }
  }
  async function setup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    if (data.get("password") !== data.get("confirm")) { setMessage("Пароли не совпадают."); return; }
    setBusy(true); setMessage("");
    try { await setupPassword(field(data, "username"), field(data, "token"), field(data, "password")); setMessage("Пароль установлен. Теперь войдите с именем пользователя и паролем."); form.reset(); }
    catch { setMessage("Не удалось установить пароль. Проверьте код, срок его действия и пароль."); }
    finally { setBusy(false); }
  }
  async function addAccount(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setMessage("");
    const secret: AccountSecret = accountKind === "app"
      ? { kind: "app", app_id: Number(field(data, "app_id")), installation_id: Number(field(data, "installation_id")), private_key: field(data, "private_key") }
      : { kind: "token", token: field(data, "token") };
    try { await postAccount(field(data, "id"), secret); form.reset(); setGithubDialog(null); setMessage("Аккаунт подключён."); refresh().catch(() => setMessage("Аккаунт подключён, но не удалось обновить список.")); }
    catch (e) { setMessage(failure(e, "Не удалось подключить аккаунт. Проверьте данные и идентификатор.")); }
    finally { clearField(form, "token"); clearField(form, "private_key"); setBusy(false); }
  }
  async function connect(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    const account = field(data, "account");
    const repository = field(data, "repository").trim();
    const generatedId = githubSourceId(repository);
    if (!generatedId) { setMessage("Укажите адрес GitHub в виде owner/repository или https://github.com/owner/repository."); return; }
    const customId = field(data, "id").trim();
    const sourceId = customId || generatedId;
    const adopted = data.has("adopt_existing");
    if (!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(sourceId) || (adopted && !customId)) { setMessage("Укажите корректный идентификатор: строчные латинские буквы, цифры, дефис или подчёркивание. Для привязки существующего источника нужен его идентификатор."); return; }
    setBusy(true); setMessage("Проверяем доступ к репозиторию и его ветке…");
    try {
      await postConnection({ id: sourceId, repository, account_id: account, branch: field(data, "branch"), write: data.has("write"), adopt_existing: adopted });
      form.reset(); setGithubDialog(null); setMessage(adopted ? "Источник привязан. Проверьте конфликты и отключите прежний локальный импорт." : `Репозиторий подключён как ${sourceId}; импорт выполняется в фоне.`); refresh().catch(() => setMessage("Репозиторий подключён, но не удалось обновить список."));
    } catch (e) { setMessage(failure(e, "Не удалось подключить репозиторий. Проверьте адрес, ветку и права аккаунта.")); }
    finally { setBusy(false); }
  }
  async function logout() {
    setBusy(true); setMessage("");
    try { await postLogout("/api/admin/session"); setStatus(false); setAccounts([]); setConnections([]); setMirrors([]); }
    catch { setMessage("Не удалось завершить сеанс."); }
    finally { setBusy(false); }
  }
  async function syncConnection(id: string) {
    setBusy(true); setMessage("");
    try {
      await sync(id);
      setMessage("Синхронизация запланирована; ожидающие изменения также могут быть отправлены.");
      refresh().catch(() => setMessage("Синхронизация запланирована, но состояние источников обновить не удалось."));
    } catch { setMessage("Не удалось запланировать синхронизацию. Проверьте состояние источника и сеанс администратора."); }
    finally { setBusy(false); }
  }
  async function pauseConnection(connection: GitHubConnection) {
    const paused = !connection.paused;
    setBusy(true); setMessage("");
    try {
      await pause(connection.id, paused);
      setMessage(paused ? "Синхронизация приостановлена. Уже начатый сетевой запрос может завершиться." : "Синхронизация возобновлена; ожидающие изменения могут быть отправлены.");
      refresh().catch(() => setMessage("Состояние изменено, но список источников обновить не удалось."));
    } catch { setMessage("Не удалось изменить состояние синхронизации. Проверьте источник и сеанс администратора."); }
    finally { setBusy(false); }
  }
  async function configureConnection(event: FormEvent<HTMLFormElement>, connection: GitHubConnection) {
    event.preventDefault(); const data = new FormData(event.currentTarget);
    setBusy(true); setMessage("");
    try {
      await reconfigure({ id: connection.id, account_id: field(data, "account"), write: data.has("write"), expected_config_revision: connection.config_revision });
      setMessage(connection.paused ? "Настройки сохранены. Источник остаётся на паузе до возобновления." : "Настройки сохранены; синхронизация проверит репозиторий и может отправить ожидающие изменения.");
      refresh().catch(() => setMessage("Настройки сохранены, но список источников обновить не удалось."));
    } catch (e) {
      setMessage(failure(e, "Не удалось изменить настройки. Проверьте аккаунт и разрешение записи."));
      refresh().catch(() => setMessage((previous) => `${previous} Не удалось обновить список.`));
    } finally { setBusy(false); }
  }
  async function configureMirror(source: string, destination: string, enabled: boolean, revision: number) {
    setBusy(true); setMessage("");
    try {
      await postMirror({ source_id: source, destination_id: destination, enabled, expected_revision: revision });
      setMessage(enabled ? "Фоновое согласование включено. Проверьте состояние и конфликты." : "Согласование приостановлено; уже ожидающие отправки изменения сохраняются.");
      await refresh();
    } catch (e) { setMessage(failure(e, "Не удалось изменить согласование. Проверьте приватность и права записи источников.")); }
    finally { setBusy(false); }
  }
  const openDialog = (kind: Exclude<GitHubDialogKind, null>) => { setMessage(""); setGithubDialog(kind); };
  const selected = connections.find((connection) => connection.id === selectedConnection) || connections[0];
  return <div className="github-connect github-page reader-access">
    <p>Видимость определяется репозиторием. Wiki использует <code>veda.yaml</code>, программа — <code>docs/</code>.</p>
    {status === false && <><LoginForm submitLabel="Войти" onLogin={login} busy={busy} /><details><summary>Первый вход — установить пароль</summary><form onSubmit={setup}><label>Имя пользователя<input name="username" autoComplete="username" required /></label><label>Одноразовый код<input name="token" type="password" autoComplete="off" required /></label><label>Новый пароль<input name="password" type="password" minLength={10} maxLength={128} autoComplete="new-password" required /></label><label>Повторите пароль<input name="confirm" type="password" minLength={10} maxLength={128} autoComplete="new-password" required /></label><button disabled={busy}>Установить пароль</button></form></details></>}
    {status === true && <>
      <div className="github-page-actions"><button type="button" className="github-action secondary" onClick={() => openDialog("account")}>Добавить аккаунт</button><button type="button" className="github-action primary" onClick={() => openDialog("repository")}>Подключить репозиторий</button></div>
      <GitHubDialog dialog={githubDialog} accounts={accounts} accountKind={accountKind} repoAccount={repoAccount} busy={busy} message={message} onAccountKind={setAccountKind} onRepoAccount={setRepoAccount} onClose={() => setGithubDialog(null)} onAddAccount={(event) => { void addAccount(event); }} onConnect={(event) => { void connect(event); }} />
      <div className="github-workspace"><Accounts accounts={accounts} connections={connections} selectedId={selected?.id} onSelect={setSelectedConnection} />
      <Connections selected={selected} accounts={accounts} busy={busy} onSync={(id) => { void syncConnection(id); }} onPause={(connection) => { void pauseConnection(connection); }} onConfigure={(event, connection) => { void configureConnection(event, connection); }} /></div>
      <Mirrors connections={connections} mirrors={mirrors} busy={busy} onConfigure={(source, destination, enabled, revision) => { void configureMirror(source, destination, enabled, revision); }} /><p>Синхронизация также отправляет ожидающие изменения, если запись в репозиторий разрешена. Во время проверки и на паузе публичные статьи источника временно скрыты; локальная копия остаётся доступной авторизованным читателям.</p>
      {sharedSession ? <p>Вход выполнен через учётную запись. Для выхода используйте профиль.</p> : <button onClick={logout} disabled={busy}>Выйти из управления</button>}
    </>}
    {message && <p role="status">{message}</p>}
  </div>;
}
