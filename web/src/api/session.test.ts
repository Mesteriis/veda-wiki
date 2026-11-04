import { test } from "vitest";
import assert from "node:assert/strict";
import { loginFromLink, type LinkFetcher, type LinkHistory } from "./session";

function fixture(href: string, response: { ok: boolean } = { ok: true }) {
  const calls: unknown[][] = [];
  const history: LinkHistory = { state: null, replaceState: (...args: unknown[]) => { calls.push(["replace", ...args]); } };
  const fetcher: LinkFetcher = async (...args: unknown[]) => { calls.push(["fetch", ...args]); return response; };
  return { calls, location: { href }, history, fetcher };
}

test("exchanges a link key only after removing it, preserving navigation", async () => {
  const f = fixture("https://wiki.example/?key=synthetic&env=all&page=article#section");
  assert.equal(await loginFromLink(f.location, f.history, f.fetcher), false);
  assert.equal(f.calls[0]![3], "/?env=all&page=article#section");
  assert.equal(f.calls[1]![1], "/api/session/login");
  assert.deepEqual(JSON.parse((f.calls[1]![2] as RequestInit).body as string), { key: "synthetic" });
});

test("missing key does nothing; duplicate or empty keys are removed without login", async () => {
  const missing = fixture("https://wiki.example/?env=all");
  assert.equal(await loginFromLink(missing.location, missing.history, missing.fetcher), false);
  assert.equal(missing.calls.length, 0);
  for (const query of ["?key=", "?key=one&key=two"]) {
    const f = fixture("https://wiki.example/" + query);
    assert.equal(await loginFromLink(f.location, f.history, f.fetcher), true);
    assert.equal(f.calls.length, 1);
    assert.equal(f.calls[0]![3], "/");
  }
});

test("failure is reported without retrying or leaking the key to another request", async () => {
  const f = fixture("https://wiki.example/?key=synthetic", { ok: false });
  assert.equal(await loginFromLink(f.location, f.history, f.fetcher), true);
  assert.equal(f.calls.length, 2);
  const broken = fixture("https://wiki.example/?key=synthetic");
  broken.history.replaceState = () => { throw Error("blocked"); };
  assert.equal(await loginFromLink(broken.location, broken.history, broken.fetcher), true);
  assert.equal(broken.calls.length, 0);
});
