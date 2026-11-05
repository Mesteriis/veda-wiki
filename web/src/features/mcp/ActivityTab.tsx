import type { McpActivity } from "../../api/mcp";
import type { AccessKey } from "../../api/types";
import { ago } from "../../lib/format";

const modeNames: Record<string, string> = { reader: "Чтение", editor: "Редактор", owner: "Владелец" };
const viaNames: Record<string, string> = { "mcp-editor": "редактор", "mcp-owner": "владелец" };

interface ActivityTabProps { activity: McpActivity; keys: AccessKey[] | null; onKeys: () => void }

export function ActivityTab({ activity, keys, onKeys }: ActivityTabProps) {
  return <div className="mcp-activity">
    <section>
      <h3>Клиенты MCP</h3>
      <p className="mcp-note">Время последнего обращения каждого режима к этому серверу. Хранится в памяти и обнуляется при перезапуске VEDA.</p>
      <div className="settings-list">{["reader", "editor", "owner"].map((mode) => {
        const seen = activity.clients.find((client) => client.mode === mode);
        return <div className="settings-row mcp-row" key={mode}><div><strong>{modeNames[mode]}</strong></div><span>{seen ? ago(seen.last_seen) : "нет обращений с запуска"}</span></div>;
      })}</div>
    </section>
    <section>
      <h3>Последние правки через MCP</h3>
      {activity.writes.length ? <div className="settings-list">{activity.writes.map((write) =>
        <div className="settings-row mcp-row" key={`${write.source_id}/${write.path}@${write.revision}`}>
          <div><a href={`/?page=${encodeURIComponent(`${write.source_id}/${write.path}`)}`}><strong>{write.path}</strong></a><small>{write.source_id} · ревизия {write.revision}</small></div>
          <span>{viaNames[write.via] ?? write.via} · {ago(write.created_at)}</span>
        </div>)}</div> : <p>Правок через MCP пока нет. Правки, сделанные до появления этой отметки, здесь не показываются.</p>}
    </section>
    <section>
      <h3>Ключи чтения</h3>
      {keys === null ? <p>Чтобы видеть ключи, войдите в управление доступом в «Настройках».</p> :
        keys.length ? <div className="settings-list">{keys.map((key) =>
          <div className="settings-row mcp-row" key={key.id}><div><strong>{key.id}</strong><small>{key.sources.join(", ")}</small></div><span>до {new Date(key.expires_at).toLocaleString("ru-RU")}</span></div>)}</div>
          : <p>Ключей пока нет.</p>}
      <button type="button" className="link-button" onClick={onKeys}>Управлять ключами</button>
    </section>
  </div>;
}
