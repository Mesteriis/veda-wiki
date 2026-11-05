/** Link to an article by its source-qualified path. */
export const pageHref = (path: string): string => "/?page=" + encodeURIComponent(path);

/** Human-readable update time of an article. */
export function updatedLabel(value: string | undefined): string {
  const date = new Date(value ?? "");
  return value && !Number.isNaN(date.getTime())
    ? `Обновлено ${new Intl.DateTimeFormat("ru-RU", { dateStyle: "medium", timeStyle: "short" }).format(date)}`
    : "Время обновления неизвестно";
}

/** "5 мин назад" style age of a timestamp. */
export function ago(iso: string, now = Date.now()): string {
  const minutes = Math.max(0, Math.round((now - Date.parse(iso)) / 60000));
  if (minutes < 1) return "только что";
  if (minutes < 60) return `${minutes} мин назад`;
  const hours = Math.round(minutes / 60);
  return hours < 48 ? `${hours} ч назад` : new Date(iso).toLocaleString("ru-RU");
}
