import { vi } from "vitest";

// jsdom has no modal dialogs: these stubs open and close them the way a
// browser reports it, including the close event the components listen to.
export function stubDialogs(): void {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value: function (this: HTMLDialogElement) { this.setAttribute("open", ""); },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      if (!this.hasAttribute("open")) return;
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    },
  });
}

export function restoreDialogs(): void {
  Reflect.deleteProperty(HTMLDialogElement.prototype, "showModal");
  Reflect.deleteProperty(HTMLDialogElement.prototype, "close");
}

/** Finds a button by its exact text, including ones inside a closed dialog. */
export function buttonNamed(scope: HTMLElement, name: string): HTMLButtonElement {
  const found = [...scope.querySelectorAll("button")].find((element) => element.textContent === name);
  if (!found) throw new Error(`no button ${name}`);
  return found;
}

export function jsonResponse(body: unknown, status = 200): Promise<Response> {
  return Promise.resolve(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
}

export type Route = (init: RequestInit | undefined) => Promise<Response>;

/** Replaces fetch with a router keyed by "METHOD /route"; unknown routes answer 404. */
export function serve(routes: Record<string, Route>) {
  const fetcher = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const route = routes[`${init?.method ?? "GET"} ${String(input)}`];
    return route ? route(init) : jsonResponse({ error: "not_found" }, 404);
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
