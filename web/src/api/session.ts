import { getJSON, postJSON } from "./http";
import type { SessionProfile, SessionStatus } from "./types";

/** The three session families; each has its own cookie and status route. */
export type SessionPrefix = "/api/session" | "/api/admin/session" | "/api/access/session";

export interface Credentials {
  username: string;
  password: string;
}

export function sessionStatus(prefix: SessionPrefix, signal?: AbortSignal): Promise<SessionStatus> {
  return getJSON<SessionStatus>(`${prefix}/status`, signal ? { signal } : {});
}

export function login(prefix: SessionPrefix, credentials: Credentials): Promise<SessionStatus> {
  return postJSON<SessionStatus>(`${prefix}/login`, credentials);
}

// The reader logout is a bare POST; the admin and access ones send an empty object.
export function logout(prefix: SessionPrefix): Promise<SessionStatus> {
  return postJSON<SessionStatus>(`${prefix}/logout`, prefix === "/api/session" ? undefined : {});
}

export function changePassword(current: string, next: string): Promise<unknown> {
  return postJSON<unknown>("/api/session/password", { current_password: current, new_password: next });
}

export function profile(signal?: AbortSignal): Promise<SessionProfile> {
  return getJSON<SessionProfile>("/api/session/profile", signal ? { signal } : {});
}

/** Sets the first administrator password from a setup token (admin sessions). */
export function setupPassword(username: string, setupToken: string, password: string): Promise<unknown> {
  return postJSON<unknown>("/api/admin/session/setup", { username, setup_token: setupToken, password });
}

export interface LinkLocation {
  href: string;
}

export interface LinkHistory {
  state: unknown;
  replaceState(data: unknown, unused: string, url?: string): void;
}

export type LinkFetcher = (url: string, init: RequestInit) => Promise<{ ok: boolean }>;

// Exchange a reader key for the existing scoped session before loading state.
// Resolves to true when a link key was present but could not be used.
export async function loginFromLink(location: LinkLocation, history: LinkHistory, fetcher: LinkFetcher): Promise<boolean> {
  const url = new URL(location.href);
  const keys = url.searchParams.getAll("key");
  if (!keys.length) return false;
  url.searchParams.delete("key");
  try {
    history.replaceState(history.state, "", url.pathname + url.search + url.hash);
    if (keys.length !== 1 || !keys[0]) return true;
    const response = await fetcher("/api/session/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key: keys[0] }),
    });
    return !response.ok;
  } catch {
    return true;
  }
}
