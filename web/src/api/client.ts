// Thin fetch wrapper for the Ecogo api. The session cookie is HttpOnly; the CSRF cookie is
// readable and is echoed in X-CSRF-Token on every unsafe request (double-submit pattern).

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public param?: string,
  ) {
    super(message);
  }
}

function csrfToken(): string {
  const m = document.cookie.match(/(?:^|;\s*)ecogo_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : "";
}

let csrfReady: Promise<unknown> | null = null;

// The first GET sets the CSRF cookie; make sure one has happened before any POST.
async function ensureCsrf() {
  if (csrfToken()) return;
  csrfReady ??= fetch("/internal/config", { credentials: "same-origin" });
  await csrfReady;
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (method !== "GET") {
    await ensureCsrf();
    headers["X-CSRF-Token"] = csrfToken();
  }
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const e = data?.error ?? {};
    throw new ApiError(res.status, e.code ?? "error", e.message ?? res.statusText, e.param);
  }
  return data as T;
}
