import { useEffect, useState } from "react";
import { listKeys } from "../../api/access";
import { statusOf } from "../../api/http";
import { activity as loadActivity, listTools, type McpActivity, type McpTool } from "../../api/mcp";
import { login as postLogin, type Credentials } from "../../api/session";
import type { AccessKey } from "../../api/types";
import { I } from "../../components/Icon";
import { useSessionBootstrap } from "../../hooks/useSessionBootstrap";
import { LoginForm } from "../session/LoginForm";
import { ActivityTab } from "./ActivityTab";
import { ConnectTab } from "./ConnectTab";
import { RestTab } from "./RestTab";
import { ToolsTab } from "./ToolsTab";

type Tab = "connect" | "tools" | "rest" | "activity";

const tabs: [Tab, string, string][] = [["connect", "Подключение агентов", "link"], ["tools", "Инструменты MCP", "adjustments"], ["rest", "REST API", "code"], ["activity", "Активность", "chart-bar"]];
const headings: Record<Tab, [string, string]> = {
  connect: ["Подключение агентов", "Готовая команда для Claude Code, Codex, OpenCode и Hermes"],
  tools: ["Инструменты MCP", "Что доступно агенту в каждом режиме; список берётся с сервера"],
  rest: ["REST API", "Основные маршруты для скриптов и агентов без MCP"],
  activity: ["Активность", "Кто обращался к VEDA через MCP и что записал"],
};

const settingsKeys = "/?settings=1";

// Connecting agents, the live MCP tool list, a REST reference and agent activity.
export function ApiMcp() {
  const [tab, setTab] = useState<Tab>("connect");
  const [tools, setTools] = useState<McpTool[]>([]);
  const [activity, setActivity] = useState<McpActivity>({ clients: [], writes: [] });
  const [keys, setKeys] = useState<AccessKey[] | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function refresh() {
    const [t, a] = await Promise.all([listTools(), loadActivity()]);
    setTools(t); setActivity(a);
    // Keys belong to the separate access session; without it the tab links to the settings.
    setKeys(await listKeys().catch((error: unknown) => {
      if (![401, 403, 404].includes(statusOf(error))) setMessage("Не удалось загрузить ключи доступа.");
      return null;
    }));
  }
  const { status, setStatus } = useSessionBootstrap("/api/admin/session", {
    unavailableMessage: "Раздел доступен на приватном адресе через HTTPS или на этом компьютере.",
    onMessage: setMessage,
    onAuthenticated: () => refresh(),
  });
  useEffect(() => {
    if (!status || tab !== "activity") return;
    const timer = setInterval(() => {
      loadActivity().then(setActivity).catch((error: unknown) => {
        if ([401, 403, 404].includes(statusOf(error))) setStatus(false);
        else setMessage("Не удалось обновить активность.");
      });
    }, 15000);
    return () => clearInterval(timer);
  }, [status, tab, setStatus]);
  async function login(credentials: Credentials) {
    setBusy(true); setMessage("");
    try { await postLogin("/api/admin/session", credentials); setStatus(true); await refresh(); }
    catch { setMessage("Не удалось войти. Проверьте данные и подключение."); }
    finally { setBusy(false); }
  }
  const openKeys = () => { window.location.assign(settingsKeys); };
  const [title, text] = headings[tab];
  return <div className="settings-page api-page reader-access">
    {status === false && <div className="panel settings-login"><p>Раздел «API и MCP» доступен администратору.</p><LoginForm submitLabel="Войти" onLogin={login} busy={busy} /></div>}
    {status === true && <>
      <div className="settings-tabs" aria-label="Разделы API и MCP">{tabs.map(([id, label, icon]) =>
        <button type="button" aria-pressed={tab === id} key={id} onClick={() => setTab(id)}><I name={icon} />{label}</button>)}</div>
      <section className="settings-section">
        <div className="settings-heading"><div><h2>{title}</h2><p>{text}</p></div></div>
        {tab === "connect" && <ConnectTab onKeys={openKeys} />}
        {tab === "tools" && <ToolsTab tools={tools} />}
        {tab === "rest" && <RestTab />}
        {tab === "activity" && <ActivityTab activity={activity} keys={keys} onKeys={openKeys} />}
      </section>
    </>}
    {message && <p role="alert">{message}</p>}
  </div>;
}
