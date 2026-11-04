import type { FormEvent } from "react";
import type { GitHubAccount, GitHubConnection } from "../../api/types";
import { githubErrorMessage, githubVisibilityLabel, rateLimitedUntil } from "../../lib/githubErrors";

const stateLabels: Record<string, string> = { pending: "Ожидает синхронизации", syncing: "Синхронизация", ready: "Синхронизирован", paused: "Приостановлен", skipped: "Нет папки docs/", blocked: "Требует внимания", error: "Ошибка синхронизации" };

interface ConnectionsProps {
  selected: GitHubConnection | undefined;
  accounts: GitHubAccount[];
  busy: boolean;
  onSync: (id: string) => void;
  onPause: (connection: GitHubConnection) => void;
  onConfigure: (event: FormEvent<HTMLFormElement>, connection: GitHubConnection) => void;
}

// State and controls of the selected repository connection.
export function Connections({ selected, accounts, busy, onSync, onPause, onConfigure }: ConnectionsProps) {
  return <section className="github-repository panel">{selected ? [selected].map((c) => {
    const retry = rateLimitedUntil(c);
    const limited = retry !== null;
    const detail = retry ? `Лимит GitHub, повтор после ${retry.toLocaleString()}` : c.last_error ? githubErrorMessage(c.last_error, 0, "Проверьте состояние источника.") : "";
    return <div key={c.id}><div className="github-breadcrumb">GitHub / {c.account_id || "Публичный доступ"} / {c.repository}</div><h2>{c.repository}</h2><p>Источник: {c.id}</p><dl className="github-facts"><div><dt>Видимость</dt><dd>{githubVisibilityLabel(c)}</dd></div><div><dt>Аккаунт</dt><dd>{c.account_id || "Без аккаунта"}</dd></div><div><dt>Состояние синхронизации</dt><dd>{stateLabels[c.state] || "Состояние неизвестно"}{detail ? ` — ${detail}` : ""}</dd></div><div><dt>Последняя проверка</dt><dd>{c.verified_at ? new Date(c.verified_at).toLocaleString("ru-RU") : "Нет данных"}</dd></div><div><dt>Право на запись</dt><dd>{c.write ? "Разрешено" : "Только чтение"}</dd></div></dl><div className="github-repo-actions"><button type="button" disabled={busy || c.paused || limited} onClick={() => onSync(c.id)} aria-label={`Синхронизировать ${c.id}`}>Синхронизировать</button> <button type="button" disabled={busy} onClick={() => onPause(c)} aria-label={`${c.paused ? "Возобновить" : "Приостановить"} синхронизацию ${c.id}`}>{c.paused ? "Возобновить" : "Приостановить"}</button><details><summary>Настроить подключение {c.id}</summary><form key={c.config_revision} onSubmit={(e) => onConfigure(e, c)}><label>Аккаунт<select name="account" defaultValue={c.account_id || ""}><option value="">Без аккаунта — только публичное чтение</option>{accounts.map((a) => <option key={a.id} value={a.id}>{a.login} ({a.id})</option>)}</select></label><label className="github-write"><input type="checkbox" name="write" defaultChecked={c.write} /> Разрешить запись в репозиторий</label><button disabled={busy}>Сохранить настройки</button></form></details></div></div>;
  }) : <p>Выберите репозиторий или подключите новый.</p>}</section>;
}
