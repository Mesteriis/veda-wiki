import { afterEach, expect, test, vi } from "vitest";
import { getJSON, HttpError, isAbort, postJSON, putJSON, statusOf } from "./http";

afterEach(() => { vi.unstubAllGlobals(); });

function stub(status: number, body: unknown) {
  const fetchMock = vi.fn<(route: string, init?: RequestInit) => Promise<Response>>(async () => new Response(JSON.stringify(body), { status }));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

test("a 409 answer becomes an HttpError carrying the status and server code", async () => {
  stub(409, { error: "source_conflict" });
  const error = await postJSON("/api/x", {}).catch((e: unknown) => e);
  expect(error).toBeInstanceOf(HttpError);
  expect((error as HttpError).status).toBe(409);
  expect((error as HttpError).code).toBe("source_conflict");
});

test("an error body that is not JSON still gives an HttpError with the status", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("<html>", { status: 502 })));
  const error = await getJSON("/api/x").catch((e: unknown) => e);
  expect((error as HttpError).status).toBe(502);
  expect((error as HttpError).code).toBeUndefined();
});

test("postJSON sends the JSON header and body, a missing body sends a bare POST", async () => {
  const mock = stub(200, { ok: true });
  await postJSON("/api/x", { a: 1 });
  expect(mock.mock.calls[0]).toEqual(["/api/x", { method: "POST", headers: { "Content-Type": "application/json" }, body: '{"a":1}' }]);
  await postJSON("/api/y", undefined);
  expect(mock.mock.calls[1]).toEqual(["/api/y", { method: "POST" }]);
});

test("putJSON uses PUT and getJSON forwards the init", async () => {
  const mock = stub(200, {});
  await putJSON("/api/x", { a: 1 });
  expect((mock.mock.calls[0]?.[1] as RequestInit).method).toBe("PUT");
  const signal = new AbortController().signal;
  await getJSON("/api/z", { signal });
  expect((mock.mock.calls[1]?.[1] as RequestInit).signal).toBe(signal);
});

test("statusOf reads a refusal status and gives 0 for a network failure", () => {
  expect(statusOf(new HttpError(409))).toBe(409);
  expect(statusOf(new TypeError("Failed to fetch"))).toBe(0);
});

test("isAbort recognises only cancelled requests", () => {
  expect(isAbort(new DOMException("aborted", "AbortError"))).toBe(true);
  expect(isAbort(new TypeError("Failed to fetch"))).toBe(false);
  expect(isAbort(null)).toBe(false);
});
