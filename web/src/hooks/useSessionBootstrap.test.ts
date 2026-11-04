// @vitest-environment jsdom
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSessionBootstrap } from "./useSessionBootstrap";

type Fetch = (url: string, init: RequestInit) => Promise<Response>;

const unavailable = "Недоступно";
const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });
let fetcher: ReturnType<typeof vi.fn<Fetch>>;

describe("useSessionBootstrap", () => {
  beforeEach(() => {
    fetcher = vi.fn<Fetch>();
    vi.stubGlobal("fetch", fetcher);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("reports a signed-in session and loads its data", async () => {
    fetcher.mockResolvedValueOnce(json({ authenticated: true, via_reader_session: true }));
    const onMessage = vi.fn<(message: string) => void>();
    const onAuthenticated = vi.fn<(signal: AbortSignal) => Promise<unknown>>().mockResolvedValue(undefined);
    const { result } = renderHook(() => useSessionBootstrap("/api/admin/session", { unavailableMessage: unavailable, onMessage, onAuthenticated }));
    expect(result.current.status).toBeNull();
    await waitFor(() => expect(result.current.status).toBe(true));
    expect(result.current.sharedSession).toBe(true);
    expect(fetcher.mock.calls[0]?.[0]).toBe("/api/admin/session/status");
    await waitFor(() => expect(onAuthenticated).toHaveBeenCalledTimes(1));
    expect(onAuthenticated.mock.calls[0]?.[0]).toBeInstanceOf(AbortSignal);
    expect(onMessage).not.toHaveBeenCalled();
  });

  it("does not load data for a signed-out session", async () => {
    fetcher.mockResolvedValueOnce(json({ authenticated: false }));
    const onAuthenticated = vi.fn<(signal: AbortSignal) => Promise<unknown>>();
    const { result } = renderHook(() => useSessionBootstrap("/api/session", { unavailableMessage: unavailable, onMessage: vi.fn(), onAuthenticated }));
    await waitFor(() => expect(result.current.status).toBe(false));
    expect(result.current.sharedSession).toBe(false);
    expect(onAuthenticated).not.toHaveBeenCalled();
  });

  it("stays silent when unmounting aborts the request", async () => {
    let signal: AbortSignal | null | undefined;
    fetcher.mockImplementationOnce((_url, init) => {
      signal = init.signal;
      return new Promise<Response>((_resolve, reject) => {
        init.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      });
    });
    const onMessage = vi.fn<(message: string) => void>();
    const { result, unmount } = renderHook(() => useSessionBootstrap("/api/access/session", { unavailableMessage: unavailable, onMessage, onAuthenticated: vi.fn() }));
    unmount();
    expect(signal?.aborted).toBe(true);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(onMessage).not.toHaveBeenCalled();
    expect(result.current.status).toBeNull();
  });

  it("shows the unavailable message when the status request fails", async () => {
    fetcher.mockRejectedValueOnce(new TypeError("network down"));
    const onMessage = vi.fn<(message: string) => void>();
    const { result } = renderHook(() => useSessionBootstrap("/api/access/session", { unavailableMessage: unavailable, onMessage, onAuthenticated: vi.fn() }));
    await waitFor(() => expect(onMessage).toHaveBeenCalledWith(unavailable));
    expect(result.current.status).toBeNull();
  });

  it("shows the unavailable message when loading the data fails", async () => {
    fetcher.mockResolvedValueOnce(json({ authenticated: true }));
    const onMessage = vi.fn<(message: string) => void>();
    const onAuthenticated = vi.fn<(signal: AbortSignal) => Promise<unknown>>().mockRejectedValue(new Error("refresh failed"));
    const { result } = renderHook(() => useSessionBootstrap("/api/admin/session", { unavailableMessage: unavailable, onMessage, onAuthenticated }));
    await waitFor(() => expect(onMessage).toHaveBeenCalledWith(unavailable));
    expect(result.current.status).toBe(true);
  });
});
