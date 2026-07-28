"use client";

import { useEffect, useState } from "react";
import { Hexagon } from "lucide-react";
import { apiGet, apiPost, ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { CenteredSpinner } from "@/components/ui/spinner";
import type { AuthConfig, Me } from "@/lib/api-types";

// Config is always answerable (/auth/config is registered even with auth off),
// but the hub might be briefly unreachable — assume the common case so the
// form still renders rather than trapping the user on a spinner.
const FALLBACK_CONFIG: AuthConfig = {
  enabled: true,
  methods: ["local"],
  forceSSO: false,
};

// Only same-origin absolute paths are safe post-login targets. A protocol-
// relative value ("//evil.host") or a "/\" also parses as an absolute URL in
// the browser, so reject those too — otherwise `?next=` is an open redirect.
function safeNext(): string {
  const next = new URLSearchParams(window.location.search).get("next");
  if (
    next &&
    next.startsWith("/") &&
    !next.startsWith("//") &&
    !next.startsWith("/\\")
  ) {
    return next;
  }
  return "/";
}

export default function LoginPage() {
  const [config, setConfig] = useState<AuthConfig | null>(null);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    apiGet<AuthConfig>("/api/v1/auth/config")
      .then((c) => active && setConfig(c))
      .catch(() => active && setConfig(FALLBACK_CONFIG));
    return () => {
      active = false;
    };
  }, []);

  // Auth is off for this install — there is nothing to sign in to. Send the
  // caller straight on rather than showing a form that can't do anything.
  useEffect(() => {
    if (config && !config.enabled) {
      window.location.assign(safeNext());
    }
  }, [config]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await apiPost<Me>("/api/v1/auth/login", { email, password });
      // Full navigation so providers/queries re-initialise with the session.
      window.location.assign(safeNext());
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setError("Invalid email or password");
      } else if (err instanceof ApiError && err.status === 429) {
        setError("Too many attempts — wait a minute and retry");
      } else {
        setError("Login failed — is the hub reachable?");
      }
      setBusy(false);
    }
  };

  // Shared field styling with the app's other forms (see alerts/channel-form).
  const inputClass =
    "h-9 w-full rounded-lg border border-neutral bg-base-100 px-3 text-sm focus-visible:outline-2 focus-visible:outline-primary";

  const showLocal = config?.methods.includes("local");

  return (
    // Full-screen overlay: the login screen owns the viewport, above the shell.
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-base-100 p-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center justify-center gap-2">
          <Hexagon className="h-6 w-6 text-primary" aria-hidden />
          <span className="text-lg font-bold tracking-tight">avuru obs</span>
        </div>
        <Card className="p-6">
          {!config || !config.enabled ? (
            <CenteredSpinner />
          ) : showLocal ? (
            <form onSubmit={submit} className="flex flex-col gap-4">
              <div>
                <h1 className="text-base font-semibold">Sign in</h1>
                <p className="mt-1 text-xs text-base-content/60">
                  Sign in to reach your observability workspace.
                </p>
              </div>
              <label className="flex flex-col gap-1 text-xs text-base-content/60">
                Email
                <input
                  className={inputClass}
                  type="email"
                  autoComplete="username"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@example.com"
                  required
                />
              </label>
              <label className="flex flex-col gap-1 text-xs text-base-content/60">
                Password
                <input
                  className={inputClass}
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="••••••••"
                  required
                />
              </label>
              {error && (
                <p className="text-xs text-error" role="alert">
                  {error}
                </p>
              )}
              <Button
                type="submit"
                variant="primary"
                className="w-full"
                disabled={busy}
              >
                {busy ? "Signing in…" : "Sign in"}
              </Button>
            </form>
          ) : (
            <p className="text-sm text-base-content/60">
              No sign-in method is configured for this workspace.
            </p>
          )}
        </Card>
      </div>
    </div>
  );
}
