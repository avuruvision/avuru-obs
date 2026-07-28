// Typed fetch wrapper for the hub API. The UI is a separate static SPA served
// SINGLE-ORIGIN with the hub (UI at `/`, `/api/*` proxied to the hub by the
// ingress/nginx). The API base is therefore same-origin by default, and
// overridable per-deployment via a window config injected by `/config.js`
// (static export forbids runtime env vars — see agent_docs/ui_patterns.md).

declare global {
  interface Window {
    __AVURU_OBS_CONFIG__?: { apiBase?: string };
  }
}

// apiBase is a prefix joined before the `/api/...` path. "" = same-origin.
function apiBase(): string {
  if (typeof window !== "undefined" && window.__AVURU_OBS_CONFIG__?.apiBase) {
    return window.__AVURU_OBS_CONFIG__.apiBase;
  }
  return "";
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

export async function apiGet<T>(
  path: string,
  params?: Record<string, string | number | undefined>,
  opts?: { project?: string },
): Promise<T> {
  // path is already "/api/v1/..."; an absolute apiBase wins, "" stays same-origin.
  const url = new URL(apiBase() + path, window.location.origin);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== "") url.searchParams.set(k, String(v));
    }
  }
  const headers: Record<string, string> = { Accept: "application/json" };
  // The hub's tenancy seam; omitted for "default" so requests stay
  // byte-identical to the pre-project UI.
  if (opts?.project && opts.project !== "default") {
    headers["X-Avuru-Tenant"] = opts.project;
  }
  const res = await fetch(url, { headers });
  return handleJSON<T>(res);
}

// apiPost sends a JSON body (used by triage writes). Same tenant header and
// error handling as apiGet.
export async function apiPost<T>(
  path: string,
  body: unknown,
  opts?: { project?: string },
): Promise<T> {
  const url = new URL(apiBase() + path, window.location.origin);
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
  };
  if (opts?.project && opts.project !== "default") {
    headers["X-Avuru-Tenant"] = opts.project;
  }
  const res = await fetch(url, { method: "POST", headers, body: JSON.stringify(body) });
  return handleJSON<T>(res);
}

// apiPut mirrors apiPost with PUT (used by channel edits).
export async function apiPut<T>(
  path: string,
  body: unknown,
  opts?: { project?: string },
): Promise<T> {
  const url = new URL(apiBase() + path, window.location.origin);
  const headers: Record<string, string> = {
    Accept: "application/json",
    "Content-Type": "application/json",
  };
  if (opts?.project && opts.project !== "default") {
    headers["X-Avuru-Tenant"] = opts.project;
  }
  const res = await fetch(url, { method: "PUT", headers, body: JSON.stringify(body) });
  return handleJSON<T>(res);
}

// apiDelete issues a DELETE; a 204 resolves to undefined (no body to parse).
export async function apiDelete(
  path: string,
  opts?: { project?: string },
): Promise<void> {
  const url = new URL(apiBase() + path, window.location.origin);
  const headers: Record<string, string> = { Accept: "application/json" };
  if (opts?.project && opts.project !== "default") {
    headers["X-Avuru-Tenant"] = opts.project;
  }
  const res = await fetch(url, { method: "DELETE", headers });
  if (res.status === 204) return;
  await handleJSON<unknown>(res);
}

// apiDownload fetches a file endpoint and saves the body to disk, browser-side:
// fetch → blob → a transient object URL on a synthetic <a download> → revoke.
// The filename comes from the response Content-Disposition when present, else
// `fallbackName`. Used by the green CSRD export (CSV/JSON); a plain GET so the
// tenant header still applies.
export async function apiDownload(
  path: string,
  fallbackName: string,
  params?: Record<string, string | number | undefined>,
  opts?: { project?: string },
): Promise<void> {
  const url = new URL(apiBase() + path, window.location.origin);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== "") url.searchParams.set(k, String(v));
    }
  }
  const headers: Record<string, string> = {};
  if (opts?.project && opts.project !== "default") {
    headers["X-Avuru-Tenant"] = opts.project;
  }
  const res = await fetch(url, { headers });
  if (!res.ok) {
    let message = res.statusText;
    try {
      const body = (await res.json()) as { error?: { message?: string } };
      message = body.error?.message ?? message;
    } catch {
      // non-JSON error body — keep statusText
    }
    throw new ApiError(res.status, message);
  }
  const blob = await res.blob();
  const name = filenameFromDisposition(res.headers.get("Content-Disposition")) ?? fallbackName;
  const href = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = href;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    // Defer the revoke: revoking synchronously right after click() races the
    // browser's save in some Firefox versions and can abort the download.
    setTimeout(() => URL.revokeObjectURL(href), 0);
  }
}

// filenameFromDisposition pulls the filename out of a Content-Disposition
// header (`attachment; filename="green-report-….csv"`), if the server set one.
function filenameFromDisposition(header: string | null): string | undefined {
  if (!header) return undefined;
  const match = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(header);
  return match ? decodeURIComponent(match[1]) : undefined;
}

async function handleJSON<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let message = res.statusText;
    try {
      const body = (await res.json()) as { error?: { message?: string } };
      message = body.error?.message ?? message;
    } catch {
      // non-JSON error body — keep statusText
    }
    throw new ApiError(res.status, message);
  }
  return (await res.json()) as T;
}
