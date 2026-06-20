// Typed fetch wrapper for the hub API. Production is same-origin (embedded
// SPA); dev proxies /api to the hub (next.config.ts rewrites).

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
  const url = new URL(path, window.location.origin);
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
