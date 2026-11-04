import { afterEach, expect, test, vi } from "vitest";
import * as access from "./access";
import * as github from "./github";
import * as session from "./session";

afterEach(() => { vi.unstubAllGlobals(); });

function stub(body: unknown = {}) {
  const fetchMock = vi.fn<(route: string, init?: RequestInit) => Promise<Response>>(async () => new Response(JSON.stringify(body)));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function sent(mock: ReturnType<typeof stub>, index = 0): [string, string | undefined, unknown] {
  const [route, init] = mock.mock.calls[index] ?? ["", undefined];
  const body = typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : undefined;
  return [route, init?.method, body];
}

test("session calls keep their routes, methods and bodies", async () => {
  const mock = stub({ authenticated: true });
  await session.sessionStatus("/api/access/session");
  await session.login("/api/admin/session", { username: "u", password: "p" });
  await session.logout("/api/session");
  await session.logout("/api/admin/session");
  await session.changePassword("a", "b");
  await session.profile();
  expect(sent(mock, 0)).toEqual(["/api/access/session/status", undefined, undefined]);
  expect(sent(mock, 1)).toEqual(["/api/admin/session/login", "POST", { username: "u", password: "p" }]);
  expect(mock.mock.calls[2]).toEqual(["/api/session/logout", { method: "POST" }]);
  expect(sent(mock, 3)).toEqual(["/api/admin/session/logout", "POST", {}]);
  expect(sent(mock, 4)).toEqual(["/api/session/password", "POST", { current_password: "a", new_password: "b" }]);
  expect(sent(mock, 5)[0]).toBe("/api/session/profile");
});

test("access calls keep their routes and null lists become empty arrays", async () => {
  const mock = stub(null);
  expect(await access.listKeys()).toEqual([]);
  expect(await access.listTeams()).toEqual([]);
  await access.createKey("k", ["s"], "2030-01-01T00:00:00.000Z");
  await access.updateKey({ id: "k", sources: ["s"], expires_at: "e", etag: "t" }, ["s2"]);
  await access.revokeKey("k");
  await access.saveUser("u", { teams: ["t"] });
  await access.saveUser("u", { disabled: true }, 3);
  await access.saveTeam("t", ["s"]);
  await access.saveTeam("t", ["s"], 2);
  expect(sent(mock, 0)[0]).toBe("/api/access/keys");
  expect(sent(mock, 2)).toEqual(["/api/access/keys", "POST", { id: "k", sources: ["s"], expires_at: "2030-01-01T00:00:00.000Z" }]);
  expect(sent(mock, 3)[2]).toEqual({ id: "k", sources: ["s2"], expires_at: "e", expected_etag: "t" });
  expect(sent(mock, 4)).toEqual(["/api/access/keys/revoke", "POST", { id: "k" }]);
  expect(sent(mock, 5)[2]).toEqual({ id: "u", teams: ["t"] });
  expect(sent(mock, 6)[2]).toEqual({ id: "u", expected_revision: 3, disabled: true });
  expect(sent(mock, 7)[2]).toEqual({ id: "t", sources: ["s"] });
  expect(sent(mock, 8)[2]).toEqual({ id: "t", sources: ["s"], expected_revision: 2 });
});

test("github calls keep their routes, methods and bodies", async () => {
  const mock = stub({});
  await github.addAccount("a", { kind: "token", token: "synthetic" });
  await github.connect({ id: "s", repository: "o/r", account_id: "a", branch: "", write: false, adopt_existing: false });
  await github.reconfigure({ id: "s", account_id: "a", write: true, expected_config_revision: 4 });
  await github.sync("s");
  await github.pause("s", true);
  await github.configureMirror({ source_id: "s", destination_id: "d", enabled: true, expected_revision: 1 });
  expect(sent(mock, 0)).toEqual(["/api/admin/github/accounts", "POST", { id: "a", kind: "token", token: "synthetic" }]);
  expect(sent(mock, 1)[0]).toBe("/api/admin/github/connections");
  expect(sent(mock, 2)).toEqual(["/api/admin/github/connections", "PUT", { id: "s", account_id: "a", write: true, expected_config_revision: 4 }]);
  expect(sent(mock, 3)).toEqual(["/api/admin/github/sync", "POST", { id: "s" }]);
  expect(sent(mock, 4)).toEqual(["/api/admin/github/pause", "POST", { id: "s", paused: true }]);
  expect(sent(mock, 5)[0]).toBe("/api/admin/github/mirrors");
});

test("github errors expose the server code", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: "rate_limited" }), { status: 503 })));
  const error = await github.listAccounts().catch((e: unknown) => e);
  expect(error).toMatchObject({ status: 503, code: "rate_limited" });
});
