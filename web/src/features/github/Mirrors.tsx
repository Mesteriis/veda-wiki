import type { FormEvent } from "react";
import type { GitHubConnection, SourceMirror } from "../../api/types";
import { field } from "../../lib/forms";

const mirrorLabels: Record<string, string> = { pending: "Ожидает", ready: "Проход завершён", syncing: "Синхронизация", blocked: "Требует внимания", error: "Ошибка", paused: "Приостановлено" };

interface MirrorsProps {
  connections: GitHubConnection[];
  mirrors: SourceMirror[];
  busy: boolean;
  onConfigure: (source: string, destination: string, enabled: boolean, revision: number) => void;
}

// Background reconciliation of a private source into a writable GitHub source.
export function Mirrors({ connections, mirrors, busy, onConfigure }: MirrorsProps) {
  function enable(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    onConfigure(field(data, "source"), field(data, "destination"), true, 0);
  }
  return <details className="github-mirrors panel"><summary>Фоновое согласование источников</summary><h3>Фоновое согласование</h3>
    <p>Приватные источники остаются раздельными. Подключённый источник должен быть полностью синхронизирован и проверен. Расхождения требуют проверки; удаление не выполняется. Для включения нужна совместимая резервная копия: формат хранилища обновится.</p>
    <form onSubmit={enable}>
      <label>Исходный источник<input name="source" required placeholder="infra" /></label>
      <label>Репозиторий<select name="destination" required defaultValue=""><option value="">Выберите источник GitHub</option>{connections.filter((c) => c.write && c.private && !mirrors.some((m) => m.destination_id === c.id)).map((c) => <option key={c.id} value={c.id}>{c.id} · {c.repository}</option>)}</select></label>
      <button disabled={busy}>Включить согласование</button>
    </form>
    {mirrors.length > 0 && <ul>{mirrors.map((m) => <li key={m.destination_id}>{m.source_id} → {m.destination_id} · {mirrorLabels[m.state] || "Состояние неизвестно"}{m.blocked ? ` · ожидают или требуют проверки: ${m.blocked}` : ""} <button type="button" disabled={busy} onClick={() => onConfigure(m.source_id, m.destination_id, !m.enabled, m.revision)}>{m.enabled ? "Приостановить" : "Возобновить"}</button>{m.blocked_paths && m.blocked_paths.length > 0 && <details><summary>Показать статьи</summary><ul>{m.blocked_paths.map((p) => <li key={p}>{p}</li>)}</ul>{m.blocked > m.blocked_paths.length && <p>Показаны первые {m.blocked_paths.length} из {m.blocked}.</p>}</details>}</li>)}</ul>}
  </details>;
}
