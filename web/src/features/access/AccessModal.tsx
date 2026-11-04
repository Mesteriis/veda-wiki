import { useEffect, useRef, type FormEvent } from "react";
import type { AccessKey, SourceChoice, WikiTeam, WikiUser } from "../../api/types";
import { I } from "../../components/Icon";

/** Which form the settings dialog shows; null keeps it closed. */
export type AccessModalState =
  | { type: "user" }
  | { type: "team" }
  | { type: "key" }
  | { type: "userTeams"; item: WikiUser }
  | { type: "password"; item: WikiUser }
  | { type: "teamSources"; item: WikiTeam }
  | { type: "keySources"; item: AccessKey }
  | null;

type Submit = (event: FormEvent<HTMLFormElement>) => void;

interface AccessModalProps {
  modal: AccessModalState;
  sources: SourceChoice[];
  teams: WikiTeam[];
  busy: boolean;
  /** A key is waiting to be saved by the administrator; no new one may be created. */
  keyPending: boolean;
  message: string;
  onClose: () => void;
  onCreateUser: Submit;
  onCreateTeam: Submit;
  onCreateKey: Submit;
  onUserTeams: (event: FormEvent<HTMLFormElement>, user: WikiUser) => void;
  onPassword: (event: FormEvent<HTMLFormElement>, user: WikiUser) => void;
  onTeamSources: (event: FormEvent<HTMLFormElement>, team: WikiTeam) => void;
  onKeySources: (event: FormEvent<HTMLFormElement>, key: AccessKey) => void;
}

const titles: Record<NonNullable<AccessModalState>["type"], string> = { user: "Новый пользователь", team: "Новая команда", key: "Новый ключ чтения", userTeams: "Изменить команды", password: "Сменить пароль", teamSources: "Источники команды", keySources: "Доступ ключа" };

function SourceBoxes({ sources, checked }: { sources: SourceChoice[]; checked?: string[] }) {
  return <>{sources.map((source) => <label className="access-source" key={source.id}><input type="checkbox" name="sources" value={source.id} defaultChecked={checked?.includes(source.id)} /> {source.title} ({source.id})</label>)}</>;
}

function TeamBoxes({ teams, checked }: { teams: WikiTeam[]; checked?: string[] }) {
  return <>{teams.map((team) => <label className="access-source" key={team.id}><input type="checkbox" name="teams" value={team.id} defaultChecked={checked?.includes(team.id)} /> {team.id}</label>)}</>;
}

// The modal with the create and edit forms of the access settings.
export function AccessModal(props: AccessModalProps) {
  const { modal, sources, teams, busy, keyPending, message, onClose } = props;
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => { if (modal) ref.current?.showModal(); else ref.current?.close(); }, [modal]);
  const close = () => ref.current?.close();
  return <dialog className="settings-modal" ref={ref} onClose={onClose} onCancel={onClose} onClick={(event) => { if (event.target === ref.current) close(); }}><div className="dialog-head"><h2>{modal ? titles[modal.type] : "Настройки"}</h2><button type="button" aria-label="Закрыть" onClick={close}><I name="x" /></button></div>
    {modal?.type === "user" && <form onSubmit={props.onCreateUser}><label>Имя пользователя<input name="id" required placeholder="reader-one" /></label><label>Начальный пароль<input name="password" type="password" minLength={10} maxLength={128} autoComplete="new-password" required /></label><p>Передайте начальный пароль безопасно; пользователь сможет его сменить.</p><fieldset><legend>Команды пользователя</legend><TeamBoxes teams={teams} /></fieldset><button disabled={busy}>Создать пользователя</button></form>}
    {modal?.type === "team" && <form onSubmit={props.onCreateTeam}><label>Идентификатор<input name="id" required placeholder="team-ops" /></label><fieldset><legend>Источники команды</legend><SourceBoxes sources={sources} /></fieldset><button disabled={busy || !sources.length}>Создать команду</button></form>}
    {modal?.type === "key" && <form onSubmit={props.onCreateKey}><label>Идентификатор<input name="id" required placeholder="agent-one" /></label><label>Срок в днях<input name="days" type="number" min="1" max="365" defaultValue="30" required /></label><fieldset><legend>Доступные источники</legend><SourceBoxes sources={sources} /></fieldset><button disabled={busy || keyPending || !sources.length}>Создать ключ</button></form>}
    {modal?.type === "userTeams" && <form onSubmit={(event) => props.onUserTeams(event, modal.item)}><fieldset><legend>Команды для {modal.item.id}</legend><TeamBoxes teams={teams} checked={modal.item.teams ?? []} /></fieldset><button disabled={busy}>Сохранить команды</button></form>}
    {modal?.type === "password" && <form onSubmit={(event) => props.onPassword(event, modal.item)}><label>Новый пароль для {modal.item.id}<input name="password" type="password" minLength={10} maxLength={128} autoComplete="new-password" required /></label><button disabled={busy}>Сменить пароль</button></form>}
    {modal?.type === "teamSources" && <form onSubmit={(event) => props.onTeamSources(event, modal.item)}><fieldset><legend>Источники для {modal.item.id}</legend><SourceBoxes sources={sources} checked={modal.item.sources ?? []} /></fieldset><button disabled={busy}>Сохранить команду</button></form>}
    {modal?.type === "keySources" && <form onSubmit={(event) => props.onKeySources(event, modal.item)}><fieldset><legend>Источники для {modal.item.id}</legend><SourceBoxes sources={sources} checked={modal.item.sources} /></fieldset><button disabled={busy}>Сохранить доступ</button></form>}
    {message && <p role="alert">{message}</p>}
  </dialog>;
}
