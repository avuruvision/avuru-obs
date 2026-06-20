"use client";

import { Suspense } from "react";
import { TimeRangePicker } from "./time-range-picker";
import { ThemeSwitch } from "./theme-switch";

export function Topbar({ title }: { title: string }) {
  return (
    <header className="flex h-14 shrink-0 items-center justify-between border-b border-neutral bg-base-100 px-5">
      <h1 className="text-base font-semibold tracking-tight">{title}</h1>
      <div className="flex items-center gap-2">
        {/* useSearchParams consumer must sit under Suspense (static export) */}
        <Suspense fallback={null}>
          <TimeRangePicker />
        </Suspense>
        <ThemeSwitch />
      </div>
    </header>
  );
}
