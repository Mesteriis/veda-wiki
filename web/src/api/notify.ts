import { getJSON, postJSON } from "./http";
import type { IssuedKey } from "./types";

/** A visitor's request for API/MCP access to public sources. */
export interface AccessRequest {
  id: string;
  sources: string[];
  telegram: string;
  comment: string;
  state: "pending" | "approved" | "rejected";
  key_id?: string;
  created_at: string;
  decided_at?: string;
}

export type NotifyEvent = "requests" | "sync" | "keys";

export interface TelegramStatus {
  configured: boolean;
  bot: string;
  events: Record<NotifyEvent, boolean>;
  error?: string;
}

/** Sends a visitor's request from the public surface. */
export function requestAccess(sources: string[], telegram: string, comment: string, website: string): Promise<unknown> {
  return postJSON<unknown>("/api/public/access-requests", { sources, telegram, comment, website });
}

export async function listRequests(): Promise<AccessRequest[]> {
  return (await getJSON<AccessRequest[] | null>("/api/access/requests")) ?? [];
}

export function approveRequest(id: string, expiresAt: string): Promise<IssuedKey> {
  return postJSON<IssuedKey>("/api/access/requests/approve", { id, expires_at: expiresAt });
}

export function rejectRequest(id: string): Promise<unknown> {
  return postJSON<unknown>("/api/access/requests/reject", { id });
}

export function telegramStatus(): Promise<TelegramStatus> {
  return getJSON<TelegramStatus>("/api/access/telegram");
}

export function setTelegramToken(token: string): Promise<{ configured: boolean; bot?: string }> {
  return postJSON("/api/access/telegram", { token });
}

export function removeTelegramToken(): Promise<unknown> {
  return postJSON("/api/access/telegram", { remove: true });
}

export function setTelegramEvent(event: NotifyEvent, enabled: boolean): Promise<unknown> {
  return postJSON("/api/access/telegram/events", { event, enabled });
}

export function sendTelegramTest(): Promise<{ delivered: number }> {
  return postJSON("/api/access/telegram/test", {});
}

/** Sets the signed-in user's own Telegram ID; 0 removes it. */
export function setOwnTelegramID(telegramID: number): Promise<unknown> {
  return postJSON("/api/session/telegram", { telegram_id: telegramID });
}
