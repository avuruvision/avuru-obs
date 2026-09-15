"use client";

import { MobileNavigationButton } from "./app-shell";
import { Suspense } from "react";
import { TimeRangePicker } from "./time-range-picker";
import { ThemeSwitch } from "./theme-switch";
import { Breadcrumbs } from "./breadcrumbs";
import { DocsLink } from "./docs-link";

// Masthead: breadcrumb trail (where you are) on the left, global controls on
// the right. Replaces the old per-page title prop with route-derived crumbs.
export function Topbar() {
  return (
    <header className="flex min-h-16 shrink-0 flex-wrap items-center justify-between gap-3 border-b border-neutral bg-base-100 px-5 py-3">
      <div className="flex min-w-0 items-center gap-2">
        <MobileNavigationButton />
        <Breadcrumbs />
        {/* Beside the crumb, not in a help menu: the question "what is this
            screen" is asked while looking at the screen. */}
        <DocsLink />
      </div>
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
