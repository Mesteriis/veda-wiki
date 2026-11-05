import { useEffect, useState } from "react";
import { approveRequest, listRequests, rejectRequest, type AccessRequest } from "../../api/notify";
import type { IssuedKey } from "../../api/types";

// The expiry of a key issued now for the given number of days.
function expiryAfter(days: number): string {
  return new Date(Date.now() + days * 86400000).toISOString();
}

const stateNames: Record<AccessRequest["state"], string> = { pending: "Ожидает", approved: "Ключ выдан", rejected: "Отклонён" };

interface RequestsTabProps {
  busy: boolean;
  setBusy: (busy: boolean) => void;
  /** Shows the issued key once and refreshes the key list. */
  onIssued: (issued: IssuedKey) => void;
}

// Visitors' requests for API/MCP access; issuing creates a read key on the requested sources.
export function RequestsTab({ busy, setBusy, onIssued }: RequestsTabProps) {
  const [requests, setRequests] = useState<AccessRequest[]>([]);
  const [days, setDays] = useState(90);
  const [message, setMessage] = useState("");
  const refresh = () => listRequests().then(setRequests).catch(() => setMessage("Не удалось загрузить запросы."));
  useEffect(() => { void refresh(); }, []);
  async function decide(request: AccessRequest, approve: boolean) {
    if (!approve && !window.confirm(`Отклонить запрос ${request.telegram}?`)) return;
    setBusy(true); setMessage("");
    try {
      if (approve) onIssued(await approveRequest(request.id, expiryAfter(days)));
      else await rejectRequest(request.id);
      await refresh();
    } catch { setMessage("Решение не подтверждено. Обновите список и проверьте ключи перед повтором."); }
    finally { setBusy(false); }
  }
  return <div className="requests-tab">
    <label className="requests-days">Срок выдаваемого ключа, дней<input type="number" min={1} max={365} value={days} onChange={(e) => setDays(Math.min(365, Math.max(1, Number(e.target.value) || 1)))} /></label>
    <div className="settings-list">{requests.length ? requests.map((request) =>
      <div className="settings-row request-row" key={request.id}>
        <div><strong>{request.telegram}</strong><small>{new Date(request.created_at).toLocaleString("ru-RU")} · {stateNames[request.state]}{request.key_id ? ` · ключ ${request.key_id}` : ""}</small></div>
        <span>{request.sources.join(", ")}</span>
        <span className="request-comment">{request.comment || "—"}</span>
        <div className="settings-row-actions">{request.state === "pending" && <>
          <button disabled={busy} onClick={() => { void decide(request, true); }}>Выдать ключ</button>
          <button disabled={busy} onClick={() => { void decide(request, false); }}>Отклонить</button>
        </>}</div>
      </div>) : <p>Запросов пока нет. Гости отправляют их кнопкой «Доступ к API и MCP» в боковой панели.</p>}</div>
    <p className="settings-note">Ключ человеку VEDA не отправляет: передайте его сами, например в Telegram.</p>
    {message && <p role="alert">{message}</p>}
  </div>;
}
