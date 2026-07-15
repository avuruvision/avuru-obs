"use client";

import { Suspense, useEffect } from "react";
import { usePathname, useSearchParams } from "next/navigation";

// Works around a Next 16 segment-cache bug in the static export (regression
// in 16.2.0 — vercel/next.js#92187, #91658; verified still broken on 16.2.9
// and 16.2.10): at hydration the router "learns" the current route and
// caches it under the build-time renderedSearch (always "" in an export) while
// storing location.href — query string included — as the entry's canonicalUrl
// (create-initial-router-state.js → discoverKnownRoute). Any later <Link> to
// that pathname with an empty query hits the poisoned entry and commits the
// stale canonicalUrl instead of the link's href, resurrecting filters the user
// already cleared (e.g. hydrate at /traces?service=a, clear, visit /logs,
// click Traces → back to /traces?service=a). Dev servers render with the real
// search so the cache key differs and the bug never shows.
//
// The guard records the href of every click that next/link turns into an SPA
// navigation, and when the router commits a different query string for that
// pathname, rewrites the URL back to what the user actually clicked. Params
// owned by the global sync components are carried over so the rewrite doesn't
// race their own re-materialization.
//
// Delete this guard once next is on a release containing the upstream fix
// (vercel/next.js#94144, first shipped in 16.3.0-canary.31) and the
// "cleared filters stay cleared" e2e test still passes.

// Params re-added to the URL after every navigation by time-range-context.tsx
// (range) and project-context.tsx (project) — never strip them here.
const STICKY_PARAMS = ["range", "project"];

// A recorded click only corrects a commit that lands promptly. Past this age
// it is discarded: better to miss one correction than to strip params from an
// unrelated later update.
const INTENT_TTL_MS = 3_000;

// Module scope is safe: the guard is mounted exactly once (in Providers).
let pendingNav: { href: string; at: number } | null = null;

function NavUrlGuardInner() {
  const pathname = usePathname();
  const search = useSearchParams().toString();

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      // Bubble phase, after next/link's own handler ran: defaultPrevented
      // means the click became an SPA navigation. Modified clicks and
      // external links fall through to the browser and never hit the router.
      if (!e.defaultPrevented || !(e.target instanceof Element)) return;
      const href = e.target.closest('a[href^="/"]')?.getAttribute("href");
      if (href) pendingNav = { href, at: Date.now() };
    };
    document.addEventListener("click", onClick);
    return () => document.removeEventListener("click", onClick);
  }, []);

  // pathname/search are only the commit signal — the router re-renders us
  // after it writes the (possibly stale) URL; window.location is ground truth
  // in the static export (see use-url-state.ts).
  useEffect(() => {
    if (!pendingNav) return;
    const intended = new URL(pendingNav.href, window.location.origin);
    // A commit for another pathname: not ours — keep the intent (the clicked
    // navigation may still be in flight) and let the TTL bound it.
    if (intended.pathname !== window.location.pathname) return;
    const expired = Date.now() - pendingNav.at > INTENT_TTL_MS;
    pendingNav = null;
    if (expired || window.location.search === intended.search) return;
    const current = new URLSearchParams(window.location.search);
    for (const key of STICKY_PARAMS) {
      const value = current.get(key);
      if (value !== null && !intended.searchParams.has(key)) {
        intended.searchParams.set(key, value);
      }
    }
    const qs = intended.searchParams.toString();
    const target = `${intended.pathname}${qs ? `?${qs}` : ""}${intended.hash}`;
    const actual = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    if (actual === target) return;
    // null state, exactly like use-url-state.ts: Next's replaceState patch
    // copies its internal tree AND syncs useSearchParams. Passing
    // window.history.state instead would carry Next's __NA marker, which the
    // patch treats as a router-internal call — URL updated, tree not synced,
    // stale filters left rendered.
    window.history.replaceState(null, "", target);
  }, [pathname, search]);

  return null;
}

export function NavUrlGuard() {
  return (
    // useSearchParams consumer must sit under Suspense (static export).
    <Suspense fallback={null}>
      <NavUrlGuardInner />
    </Suspense>
  );
}
