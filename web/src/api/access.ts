import { getJSON, postJSON } from "./http";
import type { AccessKey, IssuedKey, SourceChoice, WikiTeam, WikiUser } from "./types";

const base = "/api/access";

export async function listSources(): Promise<SourceChoice[]> {
  return (await getJSON<SourceChoice[] | null>(`${base}/sources`)) ?? [];
}

export async function listKeys(): Promise<AccessKey[]> {
  return (await getJSON<AccessKey[] | null>(`${base}/keys`)) ?? [];
}

export function createKey(id: string, sources: string[], expiresAt: string): Promise<IssuedKey> {
  return postJSON<IssuedKey>(`${base}/keys`, { id, sources, expires_at: expiresAt });
}

/** Changes a key's sources; the etag makes a stale edit fail with 409. */
export function updateKey(key: AccessKey, sources: string[]): Promise<AccessKey> {
  return postJSON<AccessKey>(`${base}/keys`, { id: key.id, sources, expires_at: key.expires_at, expected_etag: key.etag });
}

export function revokeKey(id: string): Promise<unknown> {
  return postJSON<unknown>(`${base}/keys/revoke`, { id });
}

export async function listUsers(): Promise<WikiUser[]> {
  return (await getJSON<WikiUser[] | null>(`${base}/users`)) ?? [];
}

/** Fields a user write may carry; omitted fields are left unchanged. */
export interface UserChanges {
  teams?: string[];
  password?: string;
  disabled?: boolean;
}

/** Creates a user (no revision) or updates one (expected revision from the list). */
export function saveUser(id: string, changes: UserChanges, expectedRevision?: number): Promise<WikiUser> {
  const body = expectedRevision === undefined ? { id, ...changes } : { id, expected_revision: expectedRevision, ...changes };
  return postJSON<WikiUser>(`${base}/users`, body);
}

export async function listTeams(): Promise<WikiTeam[]> {
  return (await getJSON<WikiTeam[] | null>(`${base}/teams`)) ?? [];
}

/** Creates a team (no revision) or updates one (expected revision from the list). */
export function saveTeam(id: string, sources: string[], expectedRevision?: number): Promise<WikiTeam> {
  const body = expectedRevision === undefined ? { id, sources } : { id, sources, expected_revision: expectedRevision };
  return postJSON<WikiTeam>(`${base}/teams`, body);
}
