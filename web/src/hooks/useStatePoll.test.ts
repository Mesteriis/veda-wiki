// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { StatePayload } from "../api/types";
import { useStatePoll, type StatePoll } from "./useStatePoll";

type Fetch = (url: string, init: RequestInit) => Promise<Response>;

function payload(generated: string): StatePayload {
  return {
    view: {
      GraphEdges: null, Cards: null, SourceWarnings: null, Public: false, Groups: null, Pages: null, Page: null,
      Query: "", Selected: "", Generated: generated, Withheld: 0, Count: 0, GitHubCount: 0, TOC: "",
    },
    html: "",
    toc: "",
  };
}
const ok = (body: StatePayload, etag: string) => new Response(JSON.stringify(body), { status: 200, headers: { ETag: etag } });
const status = (code: number) => new Response(null, { status: code });

let visibility: DocumentVisibilityState = "visible";
let fetcher: ReturnType<typeof vi.fn<Fetch>>;

const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
const header = (call: number) => (fetcher.mock.calls[call]?.[1].headers ?? {}) as Record<string, string>;
function setVisibility(next: DocumentVisibilityState) {
  visibility = next;
  act(() => { document.dispatchEvent(new Event("visibilitychange")); });
}

describe("useStatePoll", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    visibility = "visible";
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => visibility });
    fetcher = vi.fn<Fetch>();
    vi.stubGlobal("fetch", fetcher);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("loads at once, then polls every ten seconds with the ETag; 304 keeps the data", async () => {
    const first = payload("one");
    fetcher.mockResolvedValueOnce(ok(first, '"a"')).mockResolvedValueOnce(status(304));
    const { result } = renderHook(() => useStatePoll(null));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/state");
    expect(header(0)).toEqual({});
    expect(result.current).toEqual({ data: first, error: "" });
    const loaded = result.current.data;

    await advance(9999);
    expect(fetcher).toHaveBeenCalledTimes(1);
    await advance(1);
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(header(1)).toEqual({ "If-None-Match": '"a"' });
    expect(result.current.data).toBe(loaded);
    expect(result.current.error).toBe("");
  });

  it("clears the data and the ETag on 404", async () => {
    fetcher.mockResolvedValueOnce(ok(payload("one"), '"a"')).mockResolvedValueOnce(status(404)).mockResolvedValueOnce(status(503));
    const { result } = renderHook(() => useStatePoll(null));
    await flush();
    await advance(10000);
    expect(result.current).toEqual({ data: null, error: "Страница не найдена" });
    await advance(10000);
    expect(header(2)).toEqual({});
    expect(result.current.error).toBe("Wiki временно недоступна");
  });

  it("pauses in a hidden tab and resumes when it becomes visible", async () => {
    fetcher.mockResolvedValueOnce(ok(payload("one"), '"a"')).mockResolvedValueOnce(status(304));
    renderHook(() => useStatePoll(null));
    await flush();
    visibility = "hidden";
    await advance(60000);
    expect(fetcher).toHaveBeenCalledTimes(1);
    setVisibility("visible");
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(header(1)).toEqual({ "If-None-Match": '"a"' });
  });

  it("restarts at once when the restart key changes, keeping the ETag", async () => {
    const first = payload("one");
    fetcher.mockResolvedValueOnce(ok(first, '"a"')).mockResolvedValueOnce(status(304));
    const { result, rerender } = renderHook<StatePoll, { key: string | null }>(({ key }) => useStatePoll(key), { initialProps: { key: null } });
    await flush();
    const loaded = result.current.data;
    expect(loaded).toEqual(first);
    const firstSignal = fetcher.mock.calls[0]?.[1].signal;
    rerender({ key: "profile" });
    await flush();
    expect(firstSignal?.aborted).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(header(1)).toEqual({ "If-None-Match": '"a"' });
    expect(result.current.data).toBe(loaded);
  });

  it("aborts the request and stops polling on unmount", async () => {
    fetcher.mockImplementation(() => new Promise<Response>(() => {}));
    const { unmount } = renderHook(() => useStatePoll(null));
    await flush();
    const signal = fetcher.mock.calls[0]?.[1].signal;
    expect(signal?.aborted).toBe(false);
    unmount();
    expect(signal?.aborted).toBe(true);
    await advance(60000);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});
