"use client";
import { useEffect, useState } from "react";
import { ApiError, apiGet } from "@/lib/api";
import type { Me } from "@/lib/api-types";

/**
 * Loads the current identity once per page. isAdmin = a "*" admin grant.
 *
 * canAdminister answers the question the UI actually asks before showing an
 * administration surface, which is NOT the same as isAdmin: on an install
 * running without authentication there is no grant to read, every request is
 * already served with full rights, and gating on isAdmin alone would hide the
 * configuration screens from exactly the installs where anyone may use them.
 */
export function useAuth() {
  const [me, setMe] = useState<Me | null>(null);
  // Three states, not two: "auth is off" and "we have not asked yet" are
  // different answers, and a gate that folds them together either flickers an
  // admin surface at a viewer or hides it on an auth-less install.
  const [authEnabled, setAuthEnabled] = useState<boolean | undefined>(undefined);
  useEffect(() => {
    apiGet<Me>("/api/v1/auth/me")
      .then((m) => {
        setMe(m);
        setAuthEnabled(true);
      })
      .catch((e: unknown) => {
        setMe(null);
        // 404 is the hub saying it registered no identity routes at all —
        // authentication is off. Anything else (offline, 5xx) leaves the
        // question open, and unknown must not read as "off".
        if (e instanceof ApiError && e.status === 404) setAuthEnabled(false);
      });
  }, []);
  const isAdmin = me?.grants.some((g) => g.scope === "*" && g.role === "admin") ?? false;
  // Undefined (still loading, or the call failed) resolves to false: a surface
  // that appears late is a blink, one that appears wrongly is a promise the
  // hub will refuse.
  const canAdminister = isAdmin || authEnabled === false;
  return { me, isAdmin, authEnabled, canAdminister };
}
