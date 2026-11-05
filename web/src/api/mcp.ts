import { getJSON } from "./http";

const base = "/api/admin/mcp";

/** An MCP tool and the modes that list it. */
export interface McpTool {
  name: string;
  description: string;
  modes: ("reader" | "editor" | "owner")[];
}

/** When an MCP mode last reached this server process. */
export interface McpClientSeen {
  mode: "reader" | "editor" | "owner";
  last_seen: string;
}

/** A revision written through MCP. */
export interface McpWrite {
  source_id: string;
  path: string;
  revision: number;
  via: string;
  created_at: string;
}

export interface McpActivity {
  clients: McpClientSeen[];
  writes: McpWrite[];
}

export async function listTools(): Promise<McpTool[]> {
  return (await getJSON<{ tools: McpTool[] | null }>(`${base}/tools`)).tools ?? [];
}

export async function activity(): Promise<McpActivity> {
  const answer = await getJSON<Partial<McpActivity>>(`${base}/activity`);
  return { clients: answer.clients ?? [], writes: answer.writes ?? [] };
}
