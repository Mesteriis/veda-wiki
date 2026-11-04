import { vi } from "vitest";
import type { ArticleKnowledge, Evidence, Subgraph } from "../api/knowledgeTypes";
import type { Page, StateView } from "../api/types";
import type { View } from "../lib/view";

// Synthetic knowledge answers shaped exactly like the Go server's JSON.
export const SHA = "a".repeat(64);
export const NEW_SHA = "b".repeat(64);
export const HOSTILE_QUOTE = '<img src=x onerror="alert(1)"> billing runs on db1';

export function evidence(quote: string, path = "billing.md", sourceID = "pub"): Evidence {
  return { source_id: sourceID, path, content_sha256: SHA, pipeline_fingerprint: "kg1-test", block_id: "b2-test", quote, start: 0, end: quote.length, source_start: 0, source_end: quote.length, exact: true };
}

export const page: Page = { path: "pub/billing.md", title: "Billing", environment: "pub", sha256: SHA };

export function readyArticle(sha = SHA): ArticleKnowledge {
  const key = { source_id: "pub", path: "billing.md", content_sha256: sha };
  return {
    key, pipeline_fingerprint: "kg1-test", status: "ready",
    knowledge: {
      key,
      entities: [
        { id: "ent-billing", type: "service", name: "billing", key: "service:billing", resolution: { method: "technical_key", basis: "billing" }, mentions: 3, documents: 2 },
        { id: "ent-redis", type: "technology", name: "Redis", key: "technology:redis", resolution: { method: "candidate" }, mentions: 1, documents: 1 },
      ],
      mentions: [],
      claims: [],
      related: [{
        key: { source_id: "gh", path: "ops/page.md", content_sha256: NEW_SHA },
        reasons: [
          { kind: "shared_entity", entity_id: "ent-billing", evidence: [evidence("billing is shared", "ops/page.md", "gh")] },
          { kind: "relation", claim_id: "clm-1", relation: "runs_on", evidence: [evidence(HOSTILE_QUOTE)] },
          { kind: "candidate", entity_id: "ent-redis", evidence: [evidence("Redis maybe", "ops/page.md", "gh")] },
        ],
      }],
      truncated: false,
    },
  };
}

export function subgraph(): Subgraph {
  return {
    nodes: [
      { id: "doc-1", kind: "document", label: "billing.md", key: { source_id: "pub", path: "billing.md", content_sha256: SHA } },
      { id: "ent-billing", kind: "entity", type: "service", label: "billing" },
      { id: "ent-db1", kind: "entity", type: "host", label: "db1" },
      { id: "ent-redis", kind: "entity", type: "technology", label: "Redis" },
    ],
    edges: [
      { id: "clm-1", kind: "relation", from: "ent-billing", to: "ent-db1", relation: "runs_on", status: "extracted", evidence: [evidence(HOSTILE_QUOTE)] },
      { id: "clm-2", kind: "relation", from: "ent-billing", to: "ent-redis", relation: "uses", status: "uncertain", markers: ["plan"], evidence: [evidence("billing will use Redis")] },
      { id: "men-1", kind: "mention", from: "doc-1", to: "ent-billing", count: 2, evidence: [evidence("billing")] },
    ],
    truncated: false,
  };
}

const stateView: StateView = {
  GraphEdges: [], Cards: [], SourceWarnings: [], Public: true, Page: null, Pages: [], Query: "", Selected: "", Generated: "", Withheld: 0, Count: 1, GitHubCount: 0, TOC: "",
  Groups: [{ Name: "Публичный", ID: "pub", Count: 1, Pages: [page] }, { Name: "GitHub", ID: "gh", Count: 1, Pages: [{ ...page, path: "gh/ops/page.md", environment: "gh", title: "Ops page" }] }],
};

export const view: View = { ...stateView, SettingsView: false, GitHubView: false, GraphView: true, RecentView: false };

export function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

export type Handler = (url: URL, init: RequestInit | undefined) => Promise<Response>;

/** Replaces fetch with a handler that sees the parsed URL; returns the mock to inspect calls. */
export function serveURL(handler: Handler) {
  const fetcher = vi.fn((input: RequestInfo | URL, init?: RequestInit) => handler(new URL(String(input), "http://veda.test"), init));
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

/** A promise resolved by the test, never by the request's abort signal: it models a late answer. */
export function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

/** Every request URL the mock saw, parsed. */
export function requested(fetcher: ReturnType<typeof serveURL>): URL[] {
  return fetcher.mock.calls.map(([input]) => new URL(String(input), "http://veda.test"));
}
