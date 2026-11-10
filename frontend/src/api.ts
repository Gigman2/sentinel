// The API client. Every request carries the session's bearer token; a 401
// clears it and tells the app to show the sign-in screen.

const TOKEN_KEY = "sentinel.token";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestId: string | null,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

let unauthorized: () => void = () => {};

/** Registers what to do when the API rejects the token. */
export function onUnauthorized(fn: () => void) {
  unauthorized = fn;
}

export function getToken(): string | null {
  try {
    return sessionStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function setToken(token: string | null) {
  try {
    if (token) sessionStorage.setItem(TOKEN_KEY, token);
    else sessionStorage.removeItem(TOKEN_KEY);
  } catch {
    // Storage unavailable: the token lasts only for this page.
  }
}

export async function apiFetch<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";

  let res: Response;
  try {
    res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch {
    throw new ApiError(0, "network_error", "Could not reach the Sentinel API.", null);
  }

  if (res.status === 401) {
    setToken(null);
    unauthorized();
    throw new ApiError(401, "unauthorized", "Your token was not accepted.", res.headers.get("X-Request-ID"));
  }
  if (!res.ok) {
    const requestId = res.headers.get("X-Request-ID");
    let data: { error?: { code?: string; message?: string }; request_id?: string } | null = null;
    try {
      data = await res.json();
    } catch {
      // Not JSON: the API always answers errors in JSON, so this came from
      // something in between, such as the dev server's proxy.
    }
    if (data?.error) {
      throw new ApiError(res.status, data.error.code ?? "http_error", data.error.message ?? `Request failed (${res.status}).`, data.request_id ?? requestId);
    }
    if (res.status >= 500) {
      throw new ApiError(
        res.status,
        "api_unreachable",
        "Couldn't reach the Sentinel API. Check that it is running and that the dev server proxies to its port (PORT in .env).",
        requestId,
      );
    }
    throw new ApiError(res.status, "http_error", `Request failed (${res.status}).`, requestId);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

/** A user-facing description of an error, with its request ID. */
export function describeError(err: unknown): string {
  if (err instanceof ApiError) return err.requestId ? `${err.message} (request ${err.requestId})` : err.message;
  return "Something went wrong.";
}
