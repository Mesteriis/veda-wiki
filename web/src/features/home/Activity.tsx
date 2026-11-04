import { useState } from "react";
import { I } from "../../components/Icon";
import { Panel } from "../../components/Panel";

const tabs = ["Все", "GitHub", "API", "MCP"];

export function Activity() {
  const [tab, setTab] = useState("Все");
  return (
    <Panel
      title="Недавняя активность"
      className="activity"
      actions={
        <div className="tabs activity-tabs">
          {tabs.map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={t === tab ? "active" : ""}
            >
              {t}
            </button>
          ))}
        </div>
      }
    >
      <div className="activity-items">
        <span className="tiny-tile">
          <I name="refresh" />
        </span>
        <p>
          {tab === "Все"
            ? "База знаний загружена."
            : tab === "MCP"
              ? "MCP работает через отдельный stdio-клиент. История вызовов не записывается."
              : `Активность ${tab} пока не подключена.`}
          <small>
            {tab === "Все"
              ? "Версии статей проверяются по SHA256"
              : "Данные появятся после настройки"}
          </small>
        </p>
        <span className="activity-time">Синхронизация</span>
      </div>
    </Panel>
  );
}
