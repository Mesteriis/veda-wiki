/** A non-2xx API answer. `code` is the server's `error` field when it sent one. */
export class HttpError extends Error {
  readonly status: number;
  readonly code: string | undefined;

  constructor(status: number, code?: string, message = "request failed") {
    super(message);
    this.name = "HttpError";
    this.status = status;
    this.code = code;
  }
}

async function request<T>(route: string, init: RequestInit): Promise<T> {
  const response = await fetch(route, init);
  if (!response.ok) {
    const body: unknown = await response.json().catch(() => null);
    const code = typeof body === "object" && body !== null && "error" in body && typeof body.error === "string" ? body.error : undefined;
    throw new HttpError(response.status, code);
  }
  return (await response.json()) as T;
}

export function getJSON<T>(route: string, init: RequestInit = {}): Promise<T> {
  return request<T>(route, init);
}

function withBody(method: "POST" | "PUT", body: unknown): RequestInit {
  // No body means a bare request, as the reader logout has always been sent.
  if (body === undefined) return { method };
  return { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
}

export function postJSON<T>(route: string, body: unknown): Promise<T> {
  return request<T>(route, withBody("POST", body));
}

export function putJSON<T>(route: string, body: unknown): Promise<T> {
  return request<T>(route, withBody("PUT", body));
}

/** True for the rejection of a request cancelled through its AbortSignal. */
export function isAbort(error: unknown): boolean {
  return typeof error === "object" && error !== null && "name" in error && error.name === "AbortError";
}

/** The HTTP status of a refused request, or 0 when the request never got an answer. */
export function statusOf(error: unknown): number {
  return error instanceof HttpError ? error.status : 0;
}
