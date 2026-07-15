"use client";

import { useCallback, useSyncExternalStore } from "react";

const CHANGE_EVENT = "avuru-storage-number";

function subscribe(callback: () => void) {
  window.addEventListener("storage", callback);
  window.addEventListener(CHANGE_EVENT, callback);
  return () => {
    window.removeEventListener("storage", callback);
    window.removeEventListener(CHANGE_EVENT, callback);
  };
}

// Numeric value persisted in localStorage, hydration-safe via
// useSyncExternalStore (server snapshot = fallback, client re-reads on mount).
export function useLocalStorageNumber(
  key: string,
  fallback: number,
): [number, (v: number) => void] {
  const value = useSyncExternalStore(
    subscribe,
    () => {
      const raw = localStorage.getItem(key);
      const parsed = raw === null ? NaN : Number(raw);
      return Number.isFinite(parsed) ? parsed : fallback;
    },
    () => fallback,
  );
  const setValue = useCallback(
    (v: number) => {
      localStorage.setItem(key, String(v));
      window.dispatchEvent(new Event(CHANGE_EVENT));
    },
    [key],
  );
  return [value, setValue];
}
