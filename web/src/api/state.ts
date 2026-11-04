import { HttpError } from "./http";
import type { StatePayload } from "./types";

/** The part of a fetch Response that state polling reads. */
export interface StateResponse {
  status: number;
  ok: boolean;
  headers: { get(name: string): string | null };
  json(): Promise<unknown>;
}

export type StateFetcher = (url: string, init: RequestInit) => Promise<StateResponse>;

export type StateResult = { unchanged: true; etag: string } | { data: StatePayload; etag: string };

// A 304 is usable only when this polling cycle already holds a matching state.
export async function fetchState(fetcher: StateFetcher, url: string, signal: AbortSignal | null, etag: string): Promise<StateResult> {
  const response = await fetcher(url, { signal, headers: etag ? { "If-None-Match": etag } : {} });
  if (response.status === 304 && etag) return { unchanged: true, etag };
  if (!response.ok) {
    throw new HttpError(response.status, undefined, response.status === 404 ? "Страница не найдена" : "Wiki временно недоступна");
  }
  return { data: (await response.json()) as StatePayload, etag: response.headers.get("ETag") || "" };
}

// A hidden tab stops polling once it holds a state; the first load always runs.
export function shouldPoll(visibilityState: string, etag: string): boolean {
  return visibilityState !== "hidden" || !etag;
}
