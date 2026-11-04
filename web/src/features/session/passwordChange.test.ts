import { afterEach, describe, expect, it, vi } from "vitest";
import { passwordChangeAllowed } from "./passwordChange";

const answer = (status: number, body: unknown) => vi.fn(async () => new Response(JSON.stringify(body), { status }));

describe("passwordChangeAllowed", () => {
  afterEach(() => { vi.unstubAllGlobals(); });

  it("reads the profile flag", async () => {
    const fetcher = answer(200, { password_change: true });
    vi.stubGlobal("fetch", fetcher);
    expect(await passwordChangeAllowed()).toBe(true);
    expect(fetcher).toHaveBeenCalledWith("/api/session/profile", {});
  });

  it("treats a refused profile as no password change", async () => {
    vi.stubGlobal("fetch", answer(403, { error: "access denied" }));
    expect(await passwordChangeAllowed()).toBe(false);
    vi.stubGlobal("fetch", answer(404, null));
    expect(await passwordChangeAllowed()).toBe(false);
  });

  it("passes network failures through", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => { throw new TypeError("Failed to fetch"); }));
    await expect(passwordChangeAllowed()).rejects.toThrow(TypeError);
  });
});
