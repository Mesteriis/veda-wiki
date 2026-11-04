// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetKnowledge } from "../../api/knowledge";
import type { EntityCard } from "../../api/knowledgeTypes";
import { deferred, evidence, HOSTILE_QUOTE, json, requested, serveURL, subgraph, view } from "../../test/knowledge";
import { Graph } from "../graph/Graph";

const entities = { entities: [{ id: "ent-billing", type: "service", name: "billing", key: "service:billing", resolution: { method: "technical_key" }, mentions: 3, documents: 2 }], total: 1 };
const card: EntityCard = { ...entities.entities[0]!, resolution: { method: "technical_key" }, mention_list: [], claims: [{ id: "clm-1", relation: "runs_on", head_id: "ent-billing", tail_id: "ent-db1", head_score: .9, tail_score: .8, status: "extracted", evidence: evidence(HOSTILE_QUOTE) }],
  document_list: [{ source_id: "pub", path: "billing.md", content_sha256: "a".repeat(64) }], candidates: [], truncated: false };

function enabledServer() {
  return serveURL(async (url) => {
    if (url.pathname.endsWith("/entities")) return json(entities);
    if (url.pathname.endsWith("/graph")) return json(subgraph());
    if (url.pathname.endsWith("/entity")) return json(card);
    return json({ error: "not_found" }, 404);
  });
}

function at(search: string) {
  window.history.replaceState(null, "", "/" + search);
}

const graphRequests = (fetcher: ReturnType<typeof serveURL>) => requested(fetcher).filter((url) => url.pathname.endsWith("/graph"));

describe("graph modes", () => {
  beforeEach(() => resetKnowledge());
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); at(""); });

  it("keeps the links screen unchanged when the module is disabled", async () => {
    const fetcher = serveURL(async () => json({ error: "knowledge_disabled" }, 503));
    at("?graph=1");
    render(<Graph v={view} />);
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
    await act(async () => { await Promise.resolve(); });
    expect(screen.queryByRole("navigation", { name: "Режим графа" })).toBeNull();
    expect(screen.getByRole("region", { name: "Граф связей статей" })).toBeTruthy();
  });

  it("offers the knowledge mode once the module answers", async () => {
    enabledServer();
    at("?graph=1");
    render(<Graph v={view} />);
    const modes = await screen.findByRole("navigation", { name: "Режим графа" });
    expect(within(modes).getByRole("link", { name: "Ссылки" }).getAttribute("aria-current")).toBe("page");
    expect(within(modes).getByRole("link", { name: "Граф знаний" }).getAttribute("href")).toBe("/?graph=1&mode=knowledge");
  });

  it("explains a disabled module in knowledge mode", async () => {
    serveURL(async () => json({ error: "knowledge_disabled" }, 503));
    at("?graph=1&mode=knowledge");
    render(<Graph v={view} />);
    expect((await screen.findByText(/отключён на этом сервере/)).textContent).toMatch(/«Ссылки»/);
    expect(screen.getByRole("link", { name: "Ссылки" })).toBeTruthy();
  });
});

describe("knowledge graph", () => {
  beforeEach(() => resetKnowledge());
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); at(""); });

  it("searches entities and builds the graph around the chosen centre", async () => {
    const fetcher = enabledServer();
    at("?graph=1&mode=knowledge");
    render(<Graph v={view} />);
    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "bill" } });
    fireEvent.submit(screen.getByRole("search"));
    await waitFor(() => expect(requested(fetcher).some((url) => url.searchParams.get("q") === "bill")).toBe(true));
    fireEvent.click(await screen.findByRole("button", { name: /^billing/ }));
    expect(await screen.findByRole("button", { name: "Сервис billing, центр" })).toBeTruthy();
    expect(window.location.search).toBe("?graph=1&mode=knowledge&entity=ent-billing");
    expect(graphRequests(fetcher)[0]?.searchParams.get("entity_id")).toBe("ent-billing");
    // Documents and entities are told apart, and the text list names the direction and the type.
    expect(screen.getByRole("button", { name: "Документ billing.md" })).toBeTruthy();
    const list = screen.getByRole("region", { name: "Список связей" });
    expect(within(list).getByRole("button", { name: "billing → работает на → db1" })).toBeTruthy();
    expect(within(list).getByText("извлечено автоматически, не уверено (план) · pub/billing.md")).toBeTruthy();
  });

  it("sends the source and relation filters to the server", async () => {
    const fetcher = enabledServer();
    at("?graph=1&mode=knowledge&entity=ent-billing");
    render(<Graph v={view} />);
    await screen.findByRole("button", { name: "Сервис billing, центр" });
    expect(graphRequests(fetcher)[0]?.searchParams.has("relations")).toBe(false);
    fireEvent.change(screen.getByRole("combobox", { name: "Источник" }), { target: { value: "gh" } });
    await waitFor(() => expect(graphRequests(fetcher).at(-1)?.searchParams.get("sources")).toBe("gh"));
    expect(requested(fetcher).filter((url) => url.pathname.endsWith("/entities")).at(-1)?.searchParams.get("source_id")).toBe("gh");
    fireEvent.click(screen.getByRole("checkbox", { name: "использует" }));
    await waitFor(() => expect(graphRequests(fetcher).at(-1)?.searchParams.get("relations")).toBe("depends_on,runs_on,part_of,maintained_by,documents"));
  });

  it("moves between nodes with arrow keys and selects with Enter", async () => {
    enabledServer();
    at("?graph=1&mode=knowledge&entity=ent-billing");
    render(<Graph v={view} />);
    const centre = await screen.findByRole("button", { name: "Сервис billing, центр" });
    expect(centre.getAttribute("tabindex")).toBe("0");
    centre.focus();
    // In the radial layout the first neighbour sits straight above the centre.
    fireEvent.keyDown(centre, { key: "ArrowUp" });
    const focused = document.activeElement as Element;
    expect(focused).not.toBe(centre);
    expect(focused.getAttribute("role")).toBe("button");
    fireEvent.keyDown(focused, { key: "Enter" });
    expect(focused.getAttribute("aria-pressed")).toBe("true");
  });

  it("shows a relation card with the direction, an honest label and the quote as text", async () => {
    enabledServer();
    at("?graph=1&mode=knowledge&entity=ent-billing");
    const { container } = render(<Graph v={view} />);
    await screen.findByRole("button", { name: "Сервис billing, центр" });
    fireEvent.click(screen.getByRole("button", { name: "billing → работает на → db1" }));
    const cardRegion = screen.getByRole("region", { name: "Карточка выбранного элемента" });
    expect(within(cardRegion).getByRole("heading", { name: "billing → работает на → db1" })).toBeTruthy();
    expect(cardRegion.textContent).toContain("извлечено автоматически");
    expect(cardRegion.textContent).toContain(HOSTILE_QUOTE);
    expect(within(cardRegion).getByRole("link", { name: "pub/billing.md" }).getAttribute("href")).toBe("/?page=pub%2Fbilling.md");
    expect(container.querySelector("img[src=x]")).toBeNull();
  });

  it("loads the entity card and expands neighbours", async () => {
    const fetcher = enabledServer();
    at("?graph=1&mode=knowledge&entity=ent-billing");
    render(<Graph v={view} />);
    const cardRegion = await screen.findByRole("region", { name: "Карточка выбранного элемента" });
    await within(cardRegion).findByText("Документы");
    expect(within(cardRegion).getAllByRole("link", { name: "pub/billing.md" }).length).toBeGreaterThan(0);
    fireEvent.click(within(cardRegion).getByRole("button", { name: "Показать соседей" }));
    await waitFor(() => expect(graphRequests(fetcher)).toHaveLength(2));
    for (const [, init] of fetcher.mock.calls) expect(init?.method ?? "GET").toBe("GET");
  });

  it("drops the graph on sign-out and ignores a late answer", async () => {
    const late = deferred<Response>();
    let graphCalls = 0;
    serveURL(async (url) => {
      if (url.pathname.endsWith("/entities")) return json(entities);
      if (url.pathname.endsWith("/graph")) {
        graphCalls++;
        if (graphCalls === 1) return json(subgraph());
        return graphCalls === 2 ? late.promise : new Promise<Response>(() => undefined);
      }
      return json(card);
    });
    at("?graph=1&mode=knowledge&entity=ent-billing");
    const { container } = render(<Graph v={view} />);
    await screen.findByRole("button", { name: "Сервис billing, центр" });
    act(() => resetKnowledge());
    expect(screen.queryByRole("button", { name: "Сервис billing, центр" })).toBeNull();
    act(() => resetKnowledge());
    await act(async () => { late.resolve(json(subgraph())); await late.promise; });
    expect(container.querySelector(".kg-canvas")).toBeNull();
  });
});
