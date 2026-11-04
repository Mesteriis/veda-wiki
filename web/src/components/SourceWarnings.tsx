import type { StateView } from "../api/types";
import { observationLabel, syncWarning } from "../lib/sourceWarnings";

export function SourceWarnings({ view }: { view: StateView }) {
  const warnings = (view.SourceWarnings || []).filter((source) => !view.Page || source.ID === view.Page.environment);
  if (!warnings.length) return null;
  return <section className="source-warnings" aria-label="Состояние источников">
    {warnings.map((source) => <div className="source-warning" key={source.ID} role="status">
      <strong>{source.Name}:</strong>{" "}
      {[source.Partial && "импорт неполный", (source.Withheld ?? 0) > 0 && `удержано или недоступно: ${source.Withheld} (возможны повторы)`, source.Dirty && "в источнике есть изменения вне коммита", source.Conflicts > 0 && `конфликты: ${source.Conflicts}`, syncWarning(source.SyncIssue ?? ""), observationLabel(source.LastImportObservedAt)].filter(Boolean).join(" · ")}.
      {" "}{source.SyncIssue && "Локальная копия может быть неактуальна. "}Проверяйте актуальное состояние перед действиями.
    </div>)}
  </section>;
}
