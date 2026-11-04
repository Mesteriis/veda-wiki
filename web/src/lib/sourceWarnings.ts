export interface ConflictPage {
  unresolved_conflict?: boolean;
  conflict_resolution_blocked_by_write?: boolean;
}

export function syncWarning(issue: string): string {
  const messages: Record<string, string> = {
    error: "ошибка синхронизации GitHub",
    paused: "синхронизация GitHub на паузе",
    attention: "синхронизация GitHub требует внимания",
    checking: "идёт проверка GitHub",
    unverified: "состояние GitHub не подтверждено",
  };
  return Object.hasOwn(messages, issue) ? messages[issue] ?? "" : "";
}

export function observationLabel(value: unknown): string {
  if (typeof value !== "string" || !value) return "время последнего импорта неизвестно";
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return "время последнего импорта неизвестно";
  return `последний импорт: ${new Intl.DateTimeFormat("ru-RU", { dateStyle: "short", timeStyle: "short" }).format(timestamp)}`;
}

export function conflictWarning(page: ConflictPage | null | undefined): string {
  if (!page?.unresolved_conflict) return "";
  if (page.conflict_resolution_blocked_by_write) {
    return "У этой статьи есть конфликт с источником. Связанная отправка блокирует его разрешение: проверьте её состояние через API или MCP перед дальнейшими действиями.";
  }
  return "У этой статьи есть неразрешённый конфликт с источником. Проверьте его состояние через API или MCP перед обновлением или переводом.";
}
