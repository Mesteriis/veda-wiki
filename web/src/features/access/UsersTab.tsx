import type { WikiUser } from "../../api/types";
import { I } from "../../components/Icon";

interface UsersTabProps {
  users: WikiUser[];
  busy: boolean;
  onTeams: (user: WikiUser) => void;
  onPassword: (user: WikiUser) => void;
  onToggle: (user: WikiUser) => void;
}

export function UsersTab({ users, busy, onTeams, onPassword, onToggle }: UsersTabProps) {
  return <div className="settings-list">{users.length ? <><div className="settings-table-head" aria-hidden="true"><span>Пользователь</span><span>Роль</span><span>Команды</span><span>Состояние</span><span></span></div>{users.map((user) => <div className="settings-row" key={user.id}><div className="settings-user"><I name="user" /><strong>{user.id}</strong></div><span><span className="settings-role">{user.admin ? "Администратор" : "Читатель"}</span></span><span>{(user.teams ?? []).join(", ") || "Без команд"}</span><span><span className={`settings-status ${user.disabled ? "is-disabled" : "is-active"}`}><I name={user.disabled ? "circle" : "circle-check"} />{user.disabled ? "Отключён" : "Активен"}</span></span><div className="settings-row-actions"><button disabled={busy} onClick={() => onTeams(user)}><I name="users-group" />Команды</button><button disabled={busy} onClick={() => onPassword(user)}><I name="key" />Сменить пароль</button><button disabled={busy} onClick={() => onToggle(user)}><I name={user.disabled ? "circle-check" : "ban"} />{user.disabled ? "Включить" : "Отключить"}</button></div></div>)}</> : <p>Пользователей пока нет.</p>}</div>;
}
