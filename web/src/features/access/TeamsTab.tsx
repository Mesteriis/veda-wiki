import type { WikiTeam } from "../../api/types";

interface TeamsTabProps {
  teams: WikiTeam[];
  busy: boolean;
  onSources: (team: WikiTeam) => void;
}

export function TeamsTab({ teams, busy, onSources }: TeamsTabProps) {
  return <div className="settings-list">{teams.length ? teams.map((team) => <div className="settings-row" key={team.id}><div><strong>{team.id}</strong><small>Команда</small></div><span>{(team.sources ?? []).join(", ")}</span><span></span><div className="settings-row-actions"><button disabled={busy} onClick={() => onSources(team)}>Изменить источники</button></div></div>) : <p>Команд пока нет.</p>}</div>;
}
