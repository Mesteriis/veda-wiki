import { I } from "../../components/Icon";
import { Panel } from "../../components/Panel";

export function Statistics({ count, groups, isPublic }: { count: number; groups: number; isPublic: boolean }) {
  const stats: [icon: string, value: number | string, label: string][] = [
    ["file-text", count, "Страниц"],
    ["folder", groups, "Разделов"],
    ["shield-check", isPublic ? "Public" : "Private", "Поверхность"],
  ];
  return (
    <Panel title="База знаний" className="statistics">
      <div className="stat-grid">
        {stats.map(([icon, value, label]) => (
          <div key={label}>
            <I name={icon} />
            <span>
              <b>{value}</b>
              <small>{label}</small>
            </span>
          </div>
        ))}
      </div>
    </Panel>
  );
}
