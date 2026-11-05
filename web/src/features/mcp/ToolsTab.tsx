import type { McpTool } from "../../api/mcp";

const modeLabels: [McpTool["modes"][number], string][] = [["reader", "Чтение"], ["editor", "Редактор"], ["owner", "Владелец"]];

export function ToolsTab({ tools }: { tools: McpTool[] }) {
  if (!tools.length) return <p>Список инструментов пока не загружен.</p>;
  return <table className="mcp-table">
    <thead><tr><th>Инструмент</th><th>Назначение</th>{modeLabels.map(([, label]) => <th key={label}>{label}</th>)}</tr></thead>
    <tbody>{tools.map((tool) => <tr key={tool.name}>
      <td><code>{tool.name}</code></td>
      <td>{tool.description}</td>
      {modeLabels.map(([mode, label]) => <td key={mode} className="mcp-mode">{tool.modes.includes(mode) ? <span aria-label={`${label}: доступен`}>●</span> : <span aria-label={`${label}: нет`} className="muted">—</span>}</td>)}
    </tr>)}</tbody>
  </table>;
}
