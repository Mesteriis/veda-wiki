import { useEffect, useRef, type FormEvent } from "react";
import type { GitHubAccount } from "../../api/types";
import { I } from "../../components/Icon";

export type GitHubDialogKind = "account" | "repository" | null;
export type AccountKind = "app" | "token";

type Submit = (event: FormEvent<HTMLFormElement>) => void;

interface GitHubDialogProps {
  dialog: GitHubDialogKind;
  accounts: GitHubAccount[];
  accountKind: AccountKind;
  repoAccount: string | null;
  busy: boolean;
  message: string;
  onAccountKind: (kind: AccountKind) => void;
  onRepoAccount: (id: string) => void;
  onClose: () => void;
  onAddAccount: Submit;
  onConnect: Submit;
}

// The modal for adding a GitHub account or connecting a repository.
export function GitHubDialog({ dialog, accounts, accountKind, repoAccount, busy, message, onAccountKind, onRepoAccount, onClose, onAddAccount, onConnect }: GitHubDialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => { if (dialog) ref.current?.showModal(); else ref.current?.close(); }, [dialog]);
  const close = () => ref.current?.close();
  return <dialog className="github-action-dialog" ref={ref} onClose={onClose} onCancel={onClose} onClick={(event) => { if (event.target === ref.current) close(); }}>
    <div className="dialog-head"><h2>{dialog === "account" ? "Аккаунт GitHub" : "Подключить репозиторий"}</h2><button type="button" aria-label="Закрыть" onClick={close}><I name="x" /></button></div>
    {dialog === "account" && <form onSubmit={onAddAccount}><label>Идентификатор<input name="id" required placeholder="personal" /></label><label>Способ подключения<select value={accountKind} onChange={(e) => onAccountKind(e.target.value === "token" ? "token" : "app")}><option value="app">GitHub App</option><option value="token">Токен</option></select></label>{accountKind === "app" ? <><label>App ID<input name="app_id" type="number" min="1" step="1" required /></label><label>Installation ID<input name="installation_id" type="number" min="1" step="1" required /></label><label>Приватный ключ App<textarea name="private_key" autoComplete="off" required rows={4} /></label></> : <label>Токен<input name="token" type="password" autoComplete="off" required /></label>}<button disabled={busy}>Сохранить аккаунт</button></form>}
    {dialog === "repository" && <form onSubmit={onConnect}><label>Адрес или owner/repository<input name="repository" required placeholder="owner/repository" /></label><label>Ветка (необязательно)<input name="branch" /></label><label>Аккаунт<select name="account" value={repoAccount || ""} onChange={(event) => onRepoAccount(event.target.value)}><option value="">Без аккаунта — только публичное чтение</option>{accounts.map((a) => <option key={a.id} value={a.id}>{a.login} ({a.id})</option>)}</select></label><label className="github-write"><input type="checkbox" name="write" /> Разрешить запись в репозиторий</label><p>Идентификатор источника будет выбран автоматически; импорт и наблюдение начнутся после подключения.</p><details><summary>Дополнительные настройки источника</summary><label>Идентификатор источника<input name="id" placeholder="автоматически" /></label><label className="github-write"><input type="checkbox" name="adopt_existing" /> Привязать существующий приватный источник</label><p>Для привязки укажите его идентификатор и аккаунт. Локальные статьи сохранятся без автоматической отправки; прежний импорт нужно перенастроить.</p></details><button disabled={busy}>{busy ? "Проверяем…" : "Подключить"}</button></form>}
    {message && <p role="status">{message}</p>}
  </dialog>;
}
