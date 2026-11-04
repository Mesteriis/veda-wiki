// Read-only client of /api/v1/knowledge/. Every request belongs to the
// current browser session: resetKnowledge() (sign-in, sign-out, a change of
// user) aborts the requests in flight, and an answer that arrives for an
// earlier session is rejected as aborted, so it can never restore old data.
import { getJSON, HttpError } from "./http";
import type { ArticleKnowledge, EntityCard, EntityPage, Subgraph } from "./knowledgeTypes";

const base = "/api/v1/knowledge/";

let generation = 0;
let enabled: boolean | null = null;
const inFlight = new Set<AbortController>();
const listeners = new Set<() => void>();

function notify(): void {
  for (const listener of listeners) listener();
}

/** Changes whenever the knowledge session is reset. */
export function knowledgeGeneration(): number {
  return generation;
}

/** Whether the module answered as enabled in this session; null until a knowledge request answers. */
export function knowledgeEnabled(): boolean | null {
  return enabled;
}

export function subscribeKnowledge(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

/** Cancels every knowledge request and forgets what this session learned. */
export function resetKnowledge(): void {
  generation++;
  enabled = null;
  for (const controller of inFlight) controller.abort();
  inFlight.clear();
  notify();
}

function setEnabled(value: boolean): void {
  if (enabled === value) return;
  enabled = value;
  notify();
}

function aborted(): DOMException {
  return new DOMException("knowledge request cancelled", "AbortError");
}

// Error codes that only an enabled module sends.
const enabledCodes = new Set(["knowledge_unavailable", "timeout", "not_found", "invalid_request"]);

async function read<T>(route: string, params: URLSearchParams, signal?: AbortSignal): Promise<T> {
  const started = generation;
  const controller = new AbortController();
  const forward = () => controller.abort();
  if (signal?.aborted) controller.abort();
  signal?.addEventListener("abort", forward);
  inFlight.add(controller);
  const current = () => started === generation && !controller.signal.aborted;
  try {
    const query = params.toString();
    const result = await getJSON<T>(base + route + (query ? "?" + query : ""), { signal: controller.signal });
    if (!current()) throw aborted();
    setEnabled(true);
    return result;
  } catch (error) {
    if (!current()) throw aborted();
    if (error instanceof HttpError && error.code === "knowledge_disabled") setEnabled(false);
    else if (error instanceof HttpError && error.code !== undefined && enabledCodes.has(error.code)) setEnabled(true);
    throw error;
  } finally {
    inFlight.delete(controller);
    signal?.removeEventListener("abort", forward);
  }
}

/** Why a knowledge read failed, as the interface explains it. */
export type KnowledgeFailure = "disabled" | "unavailable" | "timeout" | "not_found" | "invalid" | "network";

export function knowledgeFailure(error: unknown): KnowledgeFailure {
  if (!(error instanceof HttpError)) return "network";
  switch (error.code) {
    case "knowledge_disabled": return "disabled";
    case "timeout": return "timeout";
    case "not_found": return "not_found";
    case "invalid_request": return "invalid";
  }
  return error.status === 404 ? "not_found" : "unavailable";
}

export interface EntitySearch {
  q?: string;
  sourceID?: string;
  type?: string;
  limit?: number;
  offset?: number;
}

function put(params: URLSearchParams, name: string, value: string | number | undefined): void {
  if (value !== undefined && value !== "") params.set(name, String(value));
}

export function searchEntities(search: EntitySearch, signal?: AbortSignal): Promise<EntityPage> {
  const params = new URLSearchParams();
  put(params, "q", search.q?.trim());
  put(params, "source_id", search.sourceID);
  put(params, "type", search.type);
  put(params, "limit", search.limit);
  put(params, "offset", search.offset || undefined);
  return read<EntityPage>("entities", params, signal);
}

export function getEntity(id: string, limit: number, signal?: AbortSignal): Promise<EntityCard> {
  return read<EntityCard>("entity", new URLSearchParams({ id, limit: String(limit) }), signal);
}

export function getArticleKnowledge(sourceID: string, path: string, limit: number, signal?: AbortSignal): Promise<ArticleKnowledge> {
  return read<ArticleKnowledge>("article", new URLSearchParams({ source_id: sourceID, path, limit: String(limit) }), signal);
}

export interface GraphSearch {
  entityID: string;
  depth?: 1 | 2;
  sources?: string[];
  relations?: string[];
  maxNodes?: number;
  maxEdges?: number;
}

export function getGraph(search: GraphSearch, signal?: AbortSignal): Promise<Subgraph> {
  const params = new URLSearchParams({ entity_id: search.entityID });
  put(params, "depth", search.depth);
  put(params, "sources", search.sources?.join(","));
  put(params, "relations", search.relations?.join(","));
  put(params, "max_nodes", search.maxNodes);
  put(params, "max_edges", search.maxEdges);
  return read<Subgraph>("graph", params, signal);
}
