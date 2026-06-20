"use client";

import { usePathname } from "next/navigation";
import { ChevronRight } from "lucide-react";
import { routeInfo } from "./nav-config";

// Kiali-style breadcrumb trail: section › page, derived from the route.
// Uses pathname only (no searchParams), so no Suspense boundary is needed.
export function Breadcrumbs() {
  const pathname = usePathname();
  const info = routeInfo(pathname);

  return (
    <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-1.5">
      <span className="shrink-0 text-sm text-base-content/50">
        {info?.section ?? "Avuru Obs"}
      </span>
      {info && (
        <>
          <ChevronRight
            className="h-3.5 w-3.5 shrink-0 text-base-content/30"
            aria-hidden
          />
          <span className="truncate text-sm font-semibold text-base-content">
            {info.label}
          </span>
        </>
      )}
    </nav>
  );
}
