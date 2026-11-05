type Role = "Чтение" | "Владелец";

interface Route { method: string; path: string; role: Role; purpose: string; body?: string }

// The routes an agent or script needs most; docs/api.md in the repository lists all of them.
const groups: [title: string, routes: Route[]][] = [
  ["Чтение", [
    { method: "GET", path: "/api/v1/sources", role: "Чтение", purpose: "Источники, доступные ключу" },
    { method: "GET", path: "/api/v1/search?q=nginx&limit=20", role: "Чтение", purpose: "Компактный поиск по статьям" },
    { method: "GET", path: "/api/v1/articles?source_id=SOURCE&path=docs/index.md", role: "Чтение", purpose: "Текущая ревизия статьи" },
    { method: "GET", path: "/api/v1/articles/excerpt?source_id=SOURCE&path=docs/index.md&offset=0&limit=16000", role: "Чтение", purpose: "Длинная статья частями" },
    { method: "GET", path: "/api/v1/context?source_id=SOURCE&path=docs/index.md", role: "Чтение", purpose: "AGENTS.md предков и состояние источника" },
  ]],
  ["Запись статей", [
    { method: "POST", path: "/api/v1/articles", role: "Владелец", purpose: "Новая статья (ожидаемая ревизия 0)", body: `{"source_id":"SOURCE","path":"docs/new.md","text":"# Заголовок","expected_revision":0}` },
    { method: "PUT", path: "/api/v1/articles", role: "Владелец", purpose: "Обновление с текущей ревизией", body: `{"source_id":"SOURCE","path":"docs/index.md","text":"# Новый текст","expected_revision":3}` },
  ]],
  ["Управление", [
    { method: "GET", path: "/api/v1/github/connections", role: "Владелец", purpose: "Подключения GitHub и их состояние" },
    { method: "POST", path: "/api/v1/github/sync", role: "Владелец", purpose: "Запланировать синхронизацию", body: `{"id":"SOURCE"}` },
    { method: "GET", path: "/api/v1/conflicts", role: "Владелец", purpose: "Нерешённые конфликты импорта" },
    { method: "GET", path: "/api/v1/access-keys", role: "Владелец", purpose: "Ключи чтения без секретов" },
  ]],
];

function curl(origin: string, route: Route): string {
  const key = route.role === "Владелец" ? "$VEDA_OWNER_KEY" : "$VEDA_READER_KEY";
  const parts = [`curl -sS -H "Authorization: Bearer ${key}"`];
  if (route.method !== "GET") parts.push(`-X ${route.method} -H "Content-Type: application/json" -d '${route.body ?? "{}"}'`);
  parts.push(`"${origin}${route.path}"`);
  return parts.join(" ");
}

export function RestTab() {
  const origin = window.location.origin;
  return <div className="mcp-rest">
    <p>Чтение — по ключу чтения, в том числе через HTTPS этого сайта. Запись и управление — только ключом владельца на loopback сервера; из браузера запись запрещена. Ответ об ошибке — <code>{`{"error":"<код>"}`}</code>, конфликт ревизий — 409.</p>
    {groups.map(([title, routes]) => <section key={title}>
      <h3>{title}</h3>
      {routes.map((route) => <details key={route.method + route.path} className="mcp-route">
        <summary><span className={`mcp-method ${route.method.toLowerCase()}`}>{route.method}</span><code>{route.path.split("?")[0]}</code><span>{route.purpose}</span><small>{route.role}</small></summary>
        <pre><code>{curl(route.role === "Владелец" ? "http://127.0.0.1:8080" : origin, route)}</code></pre>
      </details>)}
    </section>)}
    <p className="mcp-note">Полный список маршрутов — в <code>docs/api.md</code> репозитория VEDA.</p>
  </div>;
}
