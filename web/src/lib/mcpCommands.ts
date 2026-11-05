/** MCP clients the connection tab knows how to configure. */
export type AgentClient = "claude" | "codex" | "opencode" | "hermes";
/** Remote reader by address and key, reader through a local veda binary, or editor through SSH on the server. */
export type AccessMode = "remote" | "reader" | "editor";

export interface ReaderSettings {
  /** The veda binary on the agent's machine. */
  veda: string;
  /** The reader key file on the agent's machine. */
  keyFile: string;
}

export interface EditorSettings {
  /** SSH host alias of the VEDA server. */
  host: string;
  /** Optional command that enters the service environment, e.g. a container exec. */
  prefix: string;
  /** The veda binary on the server. */
  veda: string;
  /** Loopback port of the owner listener. */
  port: string;
  /** The owner key file on the server. */
  keyFile: string;
}

export const clients: [AgentClient, string][] = [["claude", "Claude Code"], ["codex", "Codex"], ["opencode", "OpenCode"], ["hermes", "Hermes"]];

export const defaultReader: ReaderSettings = { veda: "veda", keyFile: "~/.config/veda/reader.key" };
export const defaultEditor: EditorSettings = { host: "", prefix: "", veda: "veda", port: "8080", keyFile: ".veda/owner.key" };

// POSIX single quoting; plain words stay readable.
export function shellQuote(word: string): string {
  return /^[A-Za-z0-9_@%+=:,./~-]+$/.test(word) ? word : `'${word.replace(/'/g, `'"'"'`)}'`;
}

/** The program and its arguments that start the MCP server for a mode. */
export function serverCommand(mode: AccessMode, origin: string, reader: ReaderSettings, editor: EditorSettings): string[] {
  if (mode === "reader") {
    return [reader.veda || "veda", "mcp", "--url", origin, "--reader-key-file", reader.keyFile, "--allow-remote-reader-https"];
  }
  const port = /^\d{1,5}$/.test(editor.port) ? editor.port : "8080";
  // The prefix is a command the owner typed on purpose and stays as written;
  // every other part is one word on the remote shell.
  const words = [editor.veda || "veda", "mcp", "--editor", "--url", `http://127.0.0.1:${port}`, "--key-file", editor.keyFile].map(shellQuote);
  const remote = [editor.prefix.trim(), ...words].filter((part) => part !== "").join(" ");
  return ["ssh", "-T", editor.host || "<хост>", remote];
}

/** The command that registers the MCP server named veda in a client. */
export function registerCommand(client: AgentClient, server: string[]): string {
  const quoted = server.map(shellQuote).join(" ");
  switch (client) {
    case "claude":
      return `claude mcp add --scope user --transport stdio veda -- ${quoted}`;
    case "codex":
      return `codex mcp add veda -- ${quoted}`;
    case "opencode":
      return `opencode mcp add --global veda -- ${quoted}`;
    case "hermes": {
      const [program = "", ...args] = server;
      return `hermes mcp add veda --command ${shellQuote(program)} --args ${args.map(shellQuote).join(" ")}`;
    }
  }
}

/** The command that registers this site's /mcp endpoint in a client; key stays a placeholder. */
export function remoteCommand(client: AgentClient, origin: string): string[] {
  const url = `${origin}/mcp`;
  switch (client) {
    case "claude":
      return [`claude mcp add --transport http veda ${url} --header "Authorization: Bearer <ключ>"`];
    case "codex":
      return ["export VEDA_KEY='<ключ>'", `codex mcp add veda --url ${url} --bearer-token-env-var VEDA_KEY`];
    case "opencode":
      return [`opencode mcp add --global veda --url ${url} --header Authorization="Bearer <ключ>"`];
    case "hermes":
      return [`hermes mcp add veda --url ${url} --auth header`];
  }
}
