// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Group } from "../../api/types";
import { jsonResponse as json, restoreDialogs, serve, stubDialogs } from "../../test/harness";
import { RequestAccess } from "./RequestAccess";

const groups: Group[] = [{ ID: "guide", Name: "Руководства", Count: 3, Pages: null }];

describe("RequestAccess", () => {
  beforeEach(stubDialogs);
  afterEach(() => { cleanup(); restoreDialogs(); vi.unstubAllGlobals(); });

  it("sends the chosen public sections, the nick with @ and the comment", async () => {
    let body: unknown = null;
    serve({ "POST /api/public/access-requests": (init) => { body = JSON.parse(String(init?.body)); return json({ status: true }, 202); } });
    render(<RequestAccess groups={groups} />);
    fireEvent.click(screen.getByRole("button", { name: "Запросить доступ к API/MCP" }));
    fireEvent.click(screen.getByLabelText("Руководства"));
    fireEvent.change(screen.getByLabelText("Ник в Telegram"), { target: { value: "person_1" } });
    fireEvent.change(screen.getByLabelText("Комментарий"), { target: { value: "for my agent" } });
    fireEvent.click(screen.getByRole("button", { name: "Отправить запрос" }));
    await screen.findByText("Запрос отправлен владельцу. Ключ вам передадут в Telegram, если доступ одобрят.");
    expect(body).toEqual({ sources: ["guide"], telegram: "@person_1", comment: "for my agent", website: "" });
  });

  it("checks the form before sending and explains a rate limit", async () => {
    const calls = serve({ "POST /api/public/access-requests": () => json({ error: "try again later" }, 429) });
    render(<RequestAccess groups={groups} />);
    fireEvent.click(screen.getByRole("button", { name: "Запросить доступ к API/MCP" }));
    fireEvent.change(screen.getByLabelText("Ник в Telegram"), { target: { value: "@person_1" } });
    fireEvent.click(screen.getByRole("button", { name: "Отправить запрос" }));
    expect(await screen.findByText("Выберите хотя бы один раздел.")).not.toBeNull();
    fireEvent.click(screen.getByLabelText("Руководства"));
    fireEvent.change(screen.getByLabelText("Ник в Telegram"), { target: { value: "@ab" } });
    fireEvent.click(screen.getByRole("button", { name: "Отправить запрос" }));
    expect(await screen.findByText(/Укажите ник в Telegram/)).not.toBeNull();
    fireEvent.change(screen.getByLabelText("Ник в Telegram"), { target: { value: "@person_1" } });
    fireEvent.click(screen.getByRole("button", { name: "Отправить запрос" }));
    expect(await screen.findByText("Слишком много запросов. Попробуйте позже.")).not.toBeNull();
    expect(calls).toHaveBeenCalledTimes(1);
  });
});
