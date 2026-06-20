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
): Promise<T> {
  // path is already "/api/v1/..."; an absolute apiBase wins, "" stays same-origin.
  const url = new URL(apiBase() + path, window.location.origin);
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== "") url.searchParams.set(k, String(v));
    }
  }
  const res = await fetch(url, { headers: { Accept: "application/json" } });
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
