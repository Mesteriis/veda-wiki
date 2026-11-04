// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetKnowledge } from "../../api/knowledge";
import type { ArticleKnowledge as Answer } from "../../api/knowledgeTypes";
import { deferred, HOSTILE_QUOTE, json, NEW_SHA, page, readyArticle, requested, serveURL, SHA } from "../../test/knowledge";
import { ArticleKnowledge } from "./ArticleKnowledge";

const titleOf = (path: string) => (path === "gh/ops/page.md" ? "Ops page" : undefined);
const show = () => render(<ArticleKnowledge page={page} titleOf={titleOf} />);
// The text of the status line once the answer is shown (not the loading line).
const settled = () => waitFor(() => {
  const text = screen.getByRole("status").textContent ?? "";
  expect(text).not.toMatch(/^Загружаем/);
  return text;
});
const answer = (status: Answer["status"], extra: Partial<Answer> = {}): Answer => ({ key: { source_id: "pub", path: "billing.md", content_sha256: SHA }, status, ...extra });

describe("ArticleKnowledge", () => {
  beforeEach(() => resetKnowledge());
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

  it("asks for the displayed article by source and path", async () => {
    const fetcher = serveURL(async () => json(answer("processing")));
    show();
    await screen.findByText(/Статья анализируется/);
    const [url] = requested(fetcher);
    expect(url?.pathname).toBe("/api/v1/knowledge/article");
    expect(url?.searchParams.get("source_id")).toBe("pub");
    expect(url?.searchParams.get("path")).toBe("billing.md");
  });

  it("shows nothing at all when the module is disabled", async () => {
    const fetcher = serveURL(async () => json({ error: "knowledge_disabled" }, 503));
    const { container } = show();
    await waitFor(() => expect(fetcher).toHaveBeenCalled());
    await act(async () => { await Promise.resolve(); });
    expect(container.innerHTML).toBe("");
  });

  it.each([
    ["stale", /прежние связи скрыты/],
    ["unavailable", /сейчас недоступен/],
    ["too_large", /слишком большая/],
  ] as const)("explains the %s status without blocking reading", async (status, text) => {
    serveURL(async () => json(answer(status)));
    show();
    expect((await settled())).toMatch(text);
  });

  it("shows the failure code only when the server sends it (owner)", async () => {
    serveURL(async () => json(answer("failed", { error: "extractor_rejected" })));
    show();
    expect((await settled())).toBe("Автоматический анализ статьи не удался. Код: extractor_rejected.");
  });

  it("reports an outage of the module as unavailable", async () => {
    serveURL(async () => json({ error: "knowledge_unavailable" }, 503));
    show();
    expect((await settled())).toMatch(/недоступен/);
  });

  it("says when a ready article has no entities", async () => {
    serveURL(async () => json({ ...answer("ready"), knowledge: { key: answer("ready").key, entities: null, mentions: null, claims: null, related: null, truncated: false } }));
    show();
    expect((await settled())).toBe("Сущности и связанные статьи не найдены.");
  });

  it("lists entities and related articles with honest reasons, quotes as text", async () => {
    serveURL(async () => json(readyArticle()));
    const { container } = show();
    await screen.findByRole("heading", { name: "Сущности" });
    expect(screen.getByRole("heading", { name: "Связанные статьи" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "billing" }).getAttribute("href")).toBe("/?graph=1&mode=knowledge&entity=ent-billing");
    expect(screen.getByRole("link", { name: "Ops page" }).getAttribute("href")).toBe("/?page=gh%2Fops%2Fpage.md");
    const text = container.textContent ?? "";
    expect(text).toContain("Общая сущность: billing");
    expect(text).toContain("Извлечённое отношение: «работает на», извлечено автоматически");
    expect(text).toContain("Возможное совпадение: Redis — возможное совпадение, не подтверждено");
    // The hostile quote is plain text: no element was created from it.
    expect(text).toContain(HOSTILE_QUOTE);
    expect(container.querySelector("img")).toBeNull();
  });

  it("never shows knowledge of another text than the displayed one", async () => {
    serveURL(async () => json(readyArticle(NEW_SHA)));
    const { container } = show();
    expect((await settled())).toMatch(/Текст статьи изменился/);
    expect(container.textContent).not.toContain("Сущности");
  });

  it("clears its data on sign-out and a late answer cannot restore it", async () => {
    const answers: ReturnType<typeof deferred<Response>>[] = [];
    serveURL(() => {
      if (!answers.length) { answers.push(deferred<Response>()); return Promise.resolve(json(readyArticle())); }
      const next = deferred<Response>();
      answers.push(next);
      return next.promise;
    });
    const { container } = show();
    await screen.findByRole("heading", { name: "Сущности" });
    act(() => resetKnowledge()); // sign-out
    expect(container.textContent).not.toContain("billing");
    expect(answers).toHaveLength(2);
    act(() => resetKnowledge()); // another user signs in
    // The request of the signed-out session answers last; it must change nothing.
    await act(async () => { answers[1]?.resolve(json(readyArticle())); await answers[1]?.promise; });
    expect(screen.queryByRole("heading", { name: "Сущности" })).toBeNull();
    expect(container.textContent).not.toContain("billing");
  });
});
