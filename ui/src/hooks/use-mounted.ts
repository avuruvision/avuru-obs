"use client";

import { useSyncExternalStore } from "react";

const emptySubscribe = () => () => {};

// False during SSG/hydration, true after — without setState-in-effect
// (react-hooks/set-state-in-effect) and without hydration mismatch.
export function useMounted(): boolean {
  return useSyncExternalStore(
    emptySubscribe,
    () => true,
    () => false,
  );
}
