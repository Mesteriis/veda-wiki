/** Link to an article by its source-qualified path. */
export const pageHref = (path: string): string => "/?page=" + encodeURIComponent(path);

/** Human-readable update time of an article. */
export function updatedLabel(value: string | undefined): string {
  const date = new Date(value ?? "");
  return value && !Number.isNaN(date.getTime())
    ? `Обновлено ${new Intl.DateTimeFormat("ru-RU", { dateStyle: "medium", timeStyle: "short" }).format(date)}`
    : "Время обновления неизвестно";
}
