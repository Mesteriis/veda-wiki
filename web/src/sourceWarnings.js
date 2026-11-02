export function syncWarning(issue) {
  const messages = {
    error: "ошибка синхронизации GitHub",
    paused: "синхронизация GitHub на паузе",
    attention: "синхронизация GitHub требует внимания",
    checking: "идёт проверка GitHub",
    unverified: "состояние GitHub не подтверждено",
  };
  return Object.hasOwn(messages, issue) ? messages[issue] : "";
}

export function observationLabel(value) {
  if (typeof value !== "string" || !value) return "время последнего импорта неизвестно";
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return "время последнего импорта неизвестно";
  return `последний импорт: ${new Intl.DateTimeFormat("ru-RU", { dateStyle: "short", timeStyle: "short" }).format(timestamp)}`;
}
