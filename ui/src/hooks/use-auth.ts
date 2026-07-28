"use client";
import { useEffect, useState } from "react";
import { apiGet } from "@/lib/api";
import type { Me } from "@/lib/api-types";

/** Loads the current identity once per page. isAdmin = a "*" admin grant. */
export function useAuth() {
  const [me, setMe] = useState<Me | null>(null);
  useEffect(() => {
    apiGet<Me>("/api/v1/auth/me").then(setMe).catch(() => setMe(null));
  }, []);
  const isAdmin = me?.grants.some((g) => g.scope === "*" && g.role === "admin") ?? false;
  return { me, isAdmin };
}
