"use client";

import { useCallback, useSyncExternalStore } from "react";

const CHANGE_EVENT = "avuru-storage-flag";

function subscribe(callback: () => void) {
  window.addEventListener("storage", callback);
  window.addEventListener(CHANGE_EVENT, callback);
  return () => {
    window.removeEventListener("storage", callback);
    window.removeEventListener(CHANGE_EVENT, callback);
  };
}

// Boolean flag persisted in localStorage, hydration-safe via
// useSyncExternalStore (server snapshot = false, client re-reads on mount).
export function useLocalStorageFlag(key: string): [boolean, (v: boolean) => void] {
  const value = useSyncExternalStore(
    subscribe,
    () => localStorage.getItem(key) === "1",
    () => false,
  );
  const setValue = useCallback(
    (v: boolean) => {
      localStorage.setItem(key, v ? "1" : "0");
      window.dispatchEvent(new Event(CHANGE_EVENT));
    },
    [key],
  );
  return [value, setValue];
}
