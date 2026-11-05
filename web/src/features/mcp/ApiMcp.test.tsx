// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { jsonResponse as json, serve } from "../../test/harness";
import { ApiMcp } from "./ApiMcp";

const minutesAgo = (n: number) => new Date(Date.now() - n * 60000).toISOString();

function signedIn() {
  return serve({
    "GET /api/admin/session/status": () => json({ authenticated: true }),
    "GET /api/admin/mcp/tools": () => json({ tools: [
      { name: "veda_read", description: "Текущая ревизия статьи.", modes: ["reader", "editor", "owner"] },
      { name: "veda_github_connect", description: "Подключить репозиторий.", modes: ["owner"] },
    ] }),
    "GET /api/admin/mcp/activity": () => json({ clients: [{ mode: "editor", last_seen: minutesAgo(3) }], writes: [
      { source_id: "wiki", path: "docs/index.md", revision: 4, via: "mcp-editor", created_at: minutesAgo(5) },
    ] }),
    "GET /api/access/keys": () => json({ error: "not signed in" }, 401),
  });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("ApiMcp", () => {
  it("asks an anonymous visitor to sign in", async () => {
    serve({ "GET /api/admin/session/status": () => json({ authenticated: false }) });
    render(<ApiMcp />);
    await screen.findByText("Раздел «API и MCP» доступен администратору.");
  });

  it("builds a reader command for the chosen client from this site's address", async () => {
    signedIn();
    const { container } = render(<ApiMcp />);
    fireEvent.click(await screen.findByRole("button", { name: "Codex" }));
    const command = container.querySelector(".mcp-command code")?.textContent ?? "";
    expect(command).toBe(`codex mcp add veda -- veda mcp --url ${window.location.origin} --reader-key-file ~/.config/veda/reader.key --allow-remote-reader-https`);
  });

  it("builds an editor command that never contains the owner key itself", async () => {
    signedIn();
    const { container } = render(<ApiMcp />);
    fireEvent.click(await screen.findByRole("button", { name: "Редактор через SSH" }));
    fireEvent.change(screen.getByLabelText("SSH-хост сервера"), { target: { value: "pve" } });
    const command = container.querySelector(".mcp-command code")?.textContent ?? "";
    expect(command).toContain("ssh -T pve 'veda mcp --editor --url http://127.0.0.1:8080 --key-file .veda/owner.key'");
    expect(JSON.parse(localStorage.getItem("veda.mcp.connect") ?? "{}").editor.host).toBe("pve");
  });

  it("lists tools by mode and shows recent MCP activity", async () => {
    signedIn();
    render(<ApiMcp />);
    fireEvent.click(await screen.findByRole("button", { name: "Инструменты MCP" }));
    expect(await screen.findByText("veda_github_connect")).not.toBeNull();
    expect(screen.getAllByLabelText("Чтение: нет")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Активность" }));
    expect(await screen.findByText("docs/index.md")).not.toBeNull();
    expect(screen.getByText("3 мин назад")).not.toBeNull();
    expect(screen.getAllByText("нет обращений с запуска")).toHaveLength(2);
    expect(screen.getByText("Чтобы видеть ключи, войдите в управление доступом в «Настройках».")).not.toBeNull();
  });
});
