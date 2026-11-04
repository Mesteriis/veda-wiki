import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deferred, json, readyArticle, requested, serveURL } from "../test/knowledge";
import { isAbort } from "./http";
import { getArticleKnowledge, getGraph, knowledgeEnabled, knowledgeFailure, knowledgeGeneration, resetKnowledge, searchEntities } from "./knowledge";
import { login, logout } from "./session";

describe("knowledge client", () => {
  beforeEach(() => resetKnowledge());
  afterEach(() => vi.unstubAllGlobals());

  it("sends only GET reads with the documented parameters", async () => {
    const fetcher = serveURL(async (url) => json(url.pathname.endsWith("graph") ? { nodes: [], edges: [], truncated: false } : { entities: [], total: 0 }));
    await searchEntities({ q: "  db1 ", sourceID: "pub", limit: 20 });
    await getGraph({ entityID: "ent-1", sources: ["pub"], relations: ["uses", "runs_on"], maxNodes: 40, maxEdges: 80 });
    const [entities, graph] = requested(fetcher);
    expect(entities?.pathname).toBe("/api/v1/knowledge/entities");
    expect(Object.fromEntries(entities?.searchParams ?? [])).toEqual({ q: "db1", source_id: "pub", limit: "20" });
    expect(Object.fromEntries(graph?.searchParams ?? [])).toEqual({ entity_id: "ent-1", sources: "pub", relations: "uses,runs_on", max_nodes: "40", max_edges: "80" });
    for (const [, init] of fetcher.mock.calls) expect(init?.method ?? "GET").toBe("GET");
  });

  it("maps flat error codes and learns whether the module is on", async () => {
    serveURL(async () => json({ error: "knowledge_disabled" }, 503));
    const disabled = await searchEntities({ limit: 1 }).catch((error: unknown) => error);
    expect(knowledgeFailure(disabled)).toBe("disabled");
    expect(knowledgeEnabled()).toBe(false);
    serveURL(async () => json({ error: "knowledge_unavailable" }, 503));
    expect(knowledgeFailure(await searchEntities({}).catch((error: unknown) => error))).toBe("unavailable");
    expect(knowledgeEnabled()).toBe(true);
    serveURL(async () => json({ error: "timeout" }, 504));
    expect(knowledgeFailure(await searchEntities({}).catch((error: unknown) => error))).toBe("timeout");
    serveURL(async () => json({ error: "not_found" }, 404));
    expect(knowledgeFailure(await searchEntities({}).catch((error: unknown) => error))).toBe("not_found");
    expect(knowledgeFailure(new TypeError("Failed to fetch"))).toBe("network");
  });

  it("aborts requests in flight on reset and drops a late answer", async () => {
    const late = deferred<Response>();
    let signal: AbortSignal | null | undefined;
    serveURL((_url, init) => { signal = init?.signal; return late.promise; });
    const pending = getArticleKnowledge("pub", "billing.md", 50);
    const before = knowledgeGeneration();
    resetKnowledge();
    expect(knowledgeGeneration()).toBe(before + 1);
    expect(signal?.aborted).toBe(true);
    // The fake fetch ignores the abort and answers anyway, as a slow network could.
    late.resolve(json(readyArticle()));
    const outcome = await pending.catch((error: unknown) => error);
    expect(isAbort(outcome)).toBe(true);
    expect(knowledgeEnabled()).toBeNull();
  });

  it("resets knowledge on sign-out and on sign-in", async () => {
    const late = deferred<Response>();
    serveURL((url) => url.pathname.startsWith("/api/v1/knowledge/") ? late.promise : Promise.resolve(json({ authenticated: url.pathname.endsWith("login") })));
    const pending = getArticleKnowledge("pub", "billing.md", 50);
    const start = knowledgeGeneration();
    await logout("/api/session");
    expect(knowledgeGeneration()).toBeGreaterThan(start);
    late.resolve(json(readyArticle()));
    expect(isAbort(await pending.catch((error: unknown) => error))).toBe(true);
    const signedOut = knowledgeGeneration();
    await login("/api/session", { username: "reader", password: "synthetic" });
    expect(knowledgeGeneration()).toBeGreaterThan(signedOut);
  });
});
