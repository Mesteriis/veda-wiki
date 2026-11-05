import { describe, expect, it } from "vitest";
import { defaultEditor, defaultReader, registerCommand, remoteCommand, serverCommand, shellQuote } from "./mcpCommands";

describe("MCP connection commands", () => {
  it("reads over HTTPS with a reader key from any machine", () => {
    const server = serverCommand("reader", "https://veda.example", defaultReader, defaultEditor);
    expect(registerCommand("claude", server)).toBe(
      "claude mcp add --scope user --transport stdio veda -- veda mcp --url https://veda.example --reader-key-file ~/.config/veda/reader.key --allow-remote-reader-https",
    );
    expect(registerCommand("codex", server)).toBe(
      "codex mcp add veda -- veda mcp --url https://veda.example --reader-key-file ~/.config/veda/reader.key --allow-remote-reader-https",
    );
  });

  it("starts the editor on the server through SSH and quotes the remote command once", () => {
    const editor = { host: "pve", prefix: "pct exec 161 -- runuser -u wiki --", veda: "/opt/veda/veda", port: "18080", keyFile: "/var/lib/veda/owner.key" };
    const server = serverCommand("editor", "https://veda.example", defaultReader, editor);
    expect(server).toEqual(["ssh", "-T", "pve", "pct exec 161 -- runuser -u wiki -- /opt/veda/veda mcp --editor --url http://127.0.0.1:18080 --key-file /var/lib/veda/owner.key"]);
    expect(registerCommand("opencode", server)).toBe(
      "opencode mcp add --global veda -- ssh -T pve 'pct exec 161 -- runuser -u wiki -- /opt/veda/veda mcp --editor --url http://127.0.0.1:18080 --key-file /var/lib/veda/owner.key'",
    );
    expect(registerCommand("hermes", server)).toBe(
      "hermes mcp add veda --command ssh --args -T pve 'pct exec 161 -- runuser -u wiki -- /opt/veda/veda mcp --editor --url http://127.0.0.1:18080 --key-file /var/lib/veda/owner.key'",
    );
  });

  it("marks a missing host instead of inventing one", () => {
    expect(serverCommand("editor", "", defaultReader, defaultEditor)[2]).toBe("<хост>");
  });

  it("quotes single quotes safely", () => {
    expect(shellQuote("it's")).toBe(`'it'"'"'s'`);
    expect(shellQuote("/plain/path")).toBe("/plain/path");
  });
});

describe("editor command hygiene", () => {
  it("quotes remote words and ignores a port that is not a number", () => {
    const editor = { host: "pve", prefix: "", veda: "/opt/my veda/veda", port: "1;rm -rf ~", keyFile: "/var/lib/veda/owner key" };
    expect(serverCommand("editor", "", defaultReader, editor)[3]).toBe(
      "'/opt/my veda/veda' mcp --editor --url http://127.0.0.1:8080 --key-file '/var/lib/veda/owner key'",
    );
  });
});

describe("remote MCP commands", () => {
  it("connects each client to this site's /mcp with a key placeholder", () => {
    expect(remoteCommand("claude", "https://veda.example")).toEqual([`claude mcp add --transport http veda https://veda.example/mcp --header "Authorization: Bearer <ключ>"`]);
    expect(remoteCommand("codex", "https://veda.example")).toEqual(["export VEDA_KEY='<ключ>'", "codex mcp add veda --url https://veda.example/mcp --bearer-token-env-var VEDA_KEY"]);
    expect(remoteCommand("opencode", "https://veda.example")[0]).toContain(`--header Authorization="Bearer <ключ>"`);
    expect(remoteCommand("hermes", "https://veda.example")[0]).toBe("hermes mcp add veda --url https://veda.example/mcp --auth header");
  });
});
