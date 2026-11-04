// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WikiUser } from "../../api/types";
import { buttonNamed as button, jsonResponse as json, restoreDialogs, serve, stubDialogs, type Route } from "../../test/harness";
import { AccessSettings } from "./AccessSettings";

const source = { id: "wiki", title: "Wiki", kind: "wiki", visibility: "private" };

function signedIn(users: WikiUser[], extra: Record<string, Route>) {
  return serve({
    "GET /api/access/session/status": () => json({ authenticated: true }),
    "GET /api/access/sources": () => json([source]),
    "GET /api/access/keys": () => json([]),
    "GET /api/access/teams": () => json([]),
    "GET /api/access/users": () => json(users),
    ...extra,
  });
}

const readerKey = "synthetic-reader-key";

async function issueKey(container: HTMLElement) {
  fireEvent.click(await screen.findByRole("button", { name: "Ключи доступа" }));
  await waitFor(() => expect(button(container, "Создать ключ").disabled).toBe(false));
  fireEvent.click(button(container, "Создать ключ"));
  const form = container.querySelector("dialog[open] form");
  if (!(form instanceof HTMLFormElement)) throw new Error("no key form");
  fireEvent.change(within(form).getByRole("textbox", { hidden: true }), { target: { value: "agent-one" } });
  fireEvent.click(within(form).getByRole("checkbox", { hidden: true }));
  fireEvent.submit(form);
  await screen.findByText("Ключ agent-one создан");
}

function keyShown(container: HTMLElement): boolean {
  const texts = [...container.querySelectorAll("textarea")].map((area) => area.value);
  return texts.some((text) => text.includes(readerKey)) || (container.textContent ?? "").includes(readerKey);
}

describe("AccessSettings", () => {
  beforeEach(stubDialogs);
  afterEach(() => {
    cleanup();
    restoreDialogs();
    vi.unstubAllGlobals();
  });

  it("access keeps form usable after a network error", async () => {
    const fetcher = signedIn([], { "POST /api/access/keys": () => Promise.reject(new TypeError("Failed to fetch")) });
    const { container } = render(<AccessSettings />);
    fireEvent.click(await screen.findByRole("button", { name: "Ключи доступа" }));
    await waitFor(() => expect(button(container, "Создать ключ").disabled).toBe(false));
    fireEvent.click(button(container, "Создать ключ"));
    const dialog = container.querySelector("dialog");
    if (!dialog) throw new Error("no dialog");
    const form = dialog.querySelector("form");
    if (!form) throw new Error("no key form");
    fireEvent.change(within(form).getByRole("textbox", { hidden: true }), { target: { value: "agent-one" } });
    fireEvent.click(within(form).getByRole("checkbox", { hidden: true }));
    fireEvent.submit(form);
    await within(dialog).findByText("Создание не подтверждено: ключ мог быть создан. Проверьте список перед повтором.");
    expect(fetcher).toHaveBeenCalledWith("/api/access/keys", expect.objectContaining({ method: "POST" }));
    expect(button(form, "Создать ключ").disabled).toBe(false);
  });

  it("disable refusal explains last administrator", async () => {
    const admin: WikiUser = { id: "owner", admin: true, teams: [], disabled: false, revision: 3 };
    signedIn([admin], { "POST /api/access/users": () => json({ error: "conflict" }, 409) });
    const { container } = render(<AccessSettings />);
    await screen.findByText("owner");
    fireEvent.click(button(container, "Отключить"));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("Отключение отклонено: пользователь изменился или это последний активный администратор. Обновите список.");
    expect(button(container, "Отключить").disabled).toBe(false);
  });

  it("shows users without teams when the server sends null", async () => {
    signedIn([{ id: "reader", admin: false, teams: null, disabled: false, revision: 1 }], {});
    render(<AccessSettings />);
    expect(await screen.findByText("Без команд")).toBeTruthy();
  });

  it("asks for an administrator login when the session is missing", async () => {
    serve({ "GET /api/access/session/status": () => json({ authenticated: false }) });
    const { container } = render(<AccessSettings />);
    await screen.findByText("Для управления доступом войдите как администратор.");
    expect(container.querySelector("input[name=username]")).not.toBeNull();
    expect(container.querySelector("input[name=password][type=password]")).not.toBeNull();
  });

  it("shows an issued key once, blocks a second key and hides it on request", async () => {
    signedIn([], { "POST /api/access/keys": () => json({ id: "agent-one", sources: ["wiki"], expires_at: "2026-12-04T00:00:00Z", key: readerKey }) });
    const { container } = render(<AccessSettings />);
    await issueKey(container);
    expect(keyShown(container)).toBe(true);
    expect(container.querySelector("dialog[open]")).toBeNull();
    expect(button(container, "Создать ключ").disabled).toBe(true);
    fireEvent.click(button(container, "Скрыть ключ"));
    expect(keyShown(container)).toBe(false);
    expect(container.querySelector(".access-issued")).toBeNull();
    expect(button(container, "Создать ключ").disabled).toBe(false);
  });

  it("drops an issued key on logout", async () => {
    signedIn([], {
      "POST /api/access/keys": () => json({ id: "agent-one", sources: ["wiki"], expires_at: "2026-12-04T00:00:00Z", key: readerKey }),
      "POST /api/access/session/logout": () => json({ authenticated: false }),
    });
    const { container } = render(<AccessSettings />);
    await issueKey(container);
    fireEvent.click(button(container, "Выйти из управления доступом"));
    await screen.findByText("Для управления доступом войдите как администратор.");
    expect(keyShown(container)).toBe(false);
    expect(container.querySelector(".access-issued")).toBeNull();
  });

  it("shows teams without sources when the server sends null", async () => {
    signedIn([], { "GET /api/access/teams": () => json([{ id: "ops", sources: null, revision: 1 }]) });
    const { container } = render(<AccessSettings />);
    fireEvent.click(await screen.findByRole("button", { name: "Команды" }));
    const row = (await screen.findByText("ops")).closest(".settings-row");
    if (!(row instanceof HTMLElement)) throw new Error("no team row");
    expect(row.querySelector(":scope > span")?.textContent).toBe("");
    fireEvent.click(button(row, "Изменить источники"));
    const form = container.querySelector("dialog[open] form");
    if (!(form instanceof HTMLFormElement)) throw new Error("no team form");
    const box = within(form).getByRole("checkbox", { hidden: true });
    expect(box instanceof HTMLInputElement && box.checked).toBe(false);
    expect(form.textContent).toContain("Источники для ops");
  });
});
