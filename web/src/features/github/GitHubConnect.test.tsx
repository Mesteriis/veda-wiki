// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { buttonNamed as button, jsonResponse as json, restoreDialogs, serve, stubDialogs, type Route } from "../../test/harness";
import { GitHubConnect } from "./GitHubConnect";

const networkDown: Route = () => Promise.reject(new TypeError("Failed to fetch"));

function signedIn(extra: Record<string, Route>) {
  return serve({
    "GET /api/admin/session/status": () => json({ authenticated: true }),
    "GET /api/admin/github/accounts": () => json(null),
    "GET /api/admin/github/connections": () => json([]),
    "GET /api/admin/github/mirrors": () => json(null),
    ...extra,
  });
}

function input(form: HTMLFormElement, name: string): HTMLInputElement | HTMLTextAreaElement {
  const found = form.elements.namedItem(name);
  if (!(found instanceof HTMLInputElement || found instanceof HTMLTextAreaElement)) throw new Error(`no field ${name}`);
  return found;
}

function dialogOf(form: HTMLFormElement): HTMLDialogElement {
  const dialog = form.closest("dialog");
  if (!dialog) throw new Error("form outside dialog");
  return dialog;
}

async function openDialog(container: HTMLElement, opener: string): Promise<HTMLFormElement> {
  await screen.findByText("Аккаунты и репозитории ещё не подключены.");
  fireEvent.click(button(container, opener));
  const form = container.querySelector("dialog[open] form");
  if (!(form instanceof HTMLFormElement)) throw new Error("no dialog form");
  return form;
}

describe("GitHubConnect", () => {
  beforeEach(stubDialogs);
  afterEach(() => {
    cleanup();
    restoreDialogs();
    vi.unstubAllGlobals();
  });

  it("github shows login when session is missing", async () => {
    serve({ "GET /api/admin/session/status": () => json({ authenticated: false }) });
    const { container } = render(<GitHubConnect />);
    await screen.findByText("Войти");
    const login = container.querySelector("form");
    if (!login) throw new Error("no login form");
    expect(login.querySelector("input[name=username]")).not.toBeNull();
    expect(login.querySelector("input[name=password][type=password]")).not.toBeNull();
  });

  it("repository form is usable again after a network error", async () => {
    signedIn({ "POST /api/admin/github/connections": networkDown });
    const { container } = render(<GitHubConnect />);
    const form = await openDialog(container, "Подключить репозиторий");
    fireEvent.change(input(form, "repository"), { target: { value: "owner/repository" } });
    fireEvent.submit(form);
    await within(dialogOf(form)).findByText("Не удалось подключить репозиторий. Проверьте адрес, ветку и права аккаунта.");
    const submit = button(form, "Подключить");
    expect(submit.disabled).toBe(false);
  });

  it("account form clears secrets and is usable again after a network error", async () => {
    signedIn({ "POST /api/admin/github/accounts": networkDown });
    const { container } = render(<GitHubConnect />);
    const form = await openDialog(container, "Добавить аккаунт");
    fireEvent.change(within(form).getByRole("combobox", { hidden: true }), { target: { value: "token" } });
    fireEvent.change(input(form, "id"), { target: { value: "personal" } });
    fireEvent.change(input(form, "token"), { target: { value: "synthetic" } });
    fireEvent.submit(form);
    await within(dialogOf(form)).findByText("Не удалось подключить аккаунт. Проверьте данные и идентификатор.");
    expect(input(form, "token").value).toBe("");
    expect(button(form, "Сохранить аккаунт").disabled).toBe(false);
  });

  it("maps a known server code on a refused repository", async () => {
    signedIn({ "POST /api/admin/github/connections": () => json({ error: "permission_denied" }, 403) });
    const { container } = render(<GitHubConnect />);
    const form = await openDialog(container, "Подключить репозиторий");
    fireEvent.change(input(form, "repository"), { target: { value: "owner/repository" } });
    fireEvent.submit(form);
    expect(await within(dialogOf(form)).findByText("GitHub отказал в доступе. Проверьте права выбранного аккаунта.")).toBeTruthy();
  });
});
