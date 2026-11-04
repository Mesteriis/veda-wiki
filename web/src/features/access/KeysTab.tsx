import type { AccessKey } from "../../api/types";

interface KeysTabProps {
  keys: AccessKey[];
  busy: boolean;
  onSources: (key: AccessKey) => void;
  onRevoke: (id: string) => void;
}

export function KeysTab({ keys, busy, onSources, onRevoke }: KeysTabProps) {
  return <div className="settings-list">{keys.length ? keys.map((key) => <div className="settings-row" key={key.id}><div><strong>{key.id}</strong><small>Ключ чтения</small></div><span>{key.sources.join(", ")}</span><span>До {new Date(key.expires_at).toLocaleString("ru-RU")}</span><div className="settings-row-actions"><button disabled={busy} onClick={() => onSources(key)}>Изменить доступ</button><button disabled={busy} onClick={() => onRevoke(key.id)}>Отозвать</button></div></div>) : <p>Ключей пока нет.</p>}</div>;
}
