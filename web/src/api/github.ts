import { getJSON, postJSON, putJSON } from "./http";
import type { GitHubAccount, GitHubConnection, SourceMirror } from "./types";

const base = "/api/admin/github";

/** The credential of a new account: a GitHub App installation or a token. */
export type AccountSecret =
  | { kind: "app"; app_id: number; installation_id: number; private_key: string }
  | { kind: "token"; token: string };

export interface ConnectRequest {
  id: string;
  repository: string;
  account_id: string;
  branch: string;
  write: boolean;
  adopt_existing: boolean;
}

export interface ReconfigureRequest {
  id: string;
  account_id: string;
  write: boolean;
  expected_config_revision: number;
}

export interface MirrorRequest {
  source_id: string;
  destination_id: string;
  enabled: boolean;
  expected_revision: number;
}

export async function listAccounts(): Promise<GitHubAccount[]> {
  return (await getJSON<GitHubAccount[] | null>(`${base}/accounts`)) ?? [];
}

export function addAccount(id: string, secret: AccountSecret): Promise<GitHubAccount> {
  return postJSON<GitHubAccount>(`${base}/accounts`, { id, ...secret });
}

export async function listConnections(): Promise<GitHubConnection[]> {
  return (await getJSON<GitHubConnection[] | null>(`${base}/connections`)) ?? [];
}

export function connect(request: ConnectRequest): Promise<GitHubConnection> {
  return postJSON<GitHubConnection>(`${base}/connections`, request);
}

export function reconfigure(request: ReconfigureRequest): Promise<GitHubConnection> {
  return putJSON<GitHubConnection>(`${base}/connections`, request);
}

export function sync(id: string): Promise<unknown> {
  return postJSON<unknown>(`${base}/sync`, { id });
}

export function pause(id: string, paused: boolean): Promise<unknown> {
  return postJSON<unknown>(`${base}/pause`, { id, paused });
}

export async function listMirrors(): Promise<SourceMirror[]> {
  return (await getJSON<SourceMirror[] | null>(`${base}/mirrors`)) ?? [];
}

export function configureMirror(request: MirrorRequest): Promise<SourceMirror> {
  return postJSON<SourceMirror>(`${base}/mirrors`, request);
}
