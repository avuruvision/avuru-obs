"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Activity,
  ChevronsLeft,
  ChevronsRight,

  LogIn,
  LogOut,
} from "lucide-react";
import { cn } from "@/lib/cn";
import { apiPost } from "@/lib/api";
import { clearStoredProject } from "@/lib/project-context";
import { useLocalStorageFlag } from "@/hooks/use-local-storage-flag";
import { useCapabilities } from "@/hooks/use-capabilities";
import { useAuth } from "@/hooks/use-auth";
import { NAV_SECTIONS } from "./nav-config";
import { ProjectSwitcher } from "./project-switcher";

// Exact nav-item styling copied from the Settings link's inactive state so the
// auth control reads as one more entry in the primary nav.
const NAV_ITEM_CLASS =
  "flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm transition-colors text-base-content/65 hover:bg-base-300 hover:text-base-content";

// Auth affordance pinned under the nav: Sign out for a real session, Sign in
// for the anonymous fallback, and nothing when identity is unknown (auth off
// or the hub is unreachable) so the sidebar never renders a dead control.
function SidebarAuth({ collapsed }: { collapsed: boolean }) {
  const { me } = useAuth();
  if (!me) return null;

  if (me.user.anonymous) {
    return (
      <a
        href="/login"
        title={collapsed ? "Sign in" : undefined}
        aria-label="Sign in"
        className={NAV_ITEM_CLASS}
      >
        <LogIn className="h-4 w-4 shrink-0" aria-hidden />
        {!collapsed && <span className="truncate">Sign in</span>}
      </a>
    );
  }

  const signOut = async () => {
    try {
      await apiPost("/api/v1/auth/logout", {});
    } catch {
      // Navigate to login regardless — the session is cleared server-side and
      // the login page re-derives state from a fresh /auth/me.
    }
    // The next sign-in on this browser may be a different, less-privileged
    // identity — don't leave it stuck on a project only THIS identity could
    // reach (see project-context.tsx's ProjectProvider self-heal effect).
    clearStoredProject();
    // A full document load is the point here: router.push() would keep the
    // signed-out identity's React tree and query cache alive on the login page.
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination
    window.location.assign("/login");
  };

  return (
    <button
      type="button"
      onClick={() => void signOut()}
      title={me.user.email}
      aria-label="Sign out"
      className={NAV_ITEM_CLASS}
    >
      <LogOut className="h-4 w-4 shrink-0" aria-hidden />
      {!collapsed && <span className="truncate">Sign out</span>}
    </button>
  );
}

const COLLAPSE_KEY = "avuru-sidebar-collapsed";

// Coroot layout: fixed left nav, collapsible with localStorage persistence
// (hydration-safe via useSyncExternalStore — see use-local-storage-flag).
export function Sidebar({ mobile = false }: { mobile?: boolean }) {
  const pathname = usePathname();
  const [storedCollapsed, setCollapsed] = useLocalStorageFlag(COLLAPSE_KEY);
  const collapsed = !mobile && storedCollapsed;
  const toggle = () => setCollapsed(!collapsed);
  const { data: capabilities } = useCapabilities();

  // Hide entries whose module this install doesn't run. Until capabilities
  // are known (or against a hub without the endpoint) everything shows —
  // see useModuleEnabled for why "show" is the safe unknown.
  const sections = NAV_SECTIONS.map((section) => ({
    ...section,
    items: section.items.filter(
      (item) => !item.module || !capabilities || capabilities.modules.includes(item.module),
    ),
  })).filter((section) => section.items.length > 0);

  return (
    <aside
      className={cn(
        "flex h-dvh shrink-0 flex-col border-r border-neutral bg-base-200 transition-[width] duration-150",
        mobile ? "w-full" : collapsed ? "w-16" : "w-56",
      )}
    >
      <Link
        href="/service-map"
        aria-label="Avuru Obs service map"
        className="flex h-16 items-center gap-2 border-b border-neutral px-4"
      >
        <Activity className="h-7 w-7 shrink-0 text-primary" aria-hidden />
        {!collapsed && (
          <span className="text-xl font-semibold tracking-tight">avuru <span className="font-normal">obs</span></span>
        )}
      </Link>

      <nav aria-label="Primary" className="flex flex-1 flex-col gap-1 overflow-y-auto p-2">
        {sections.map((section) => (
          <div key={section.title} className="flex flex-col gap-0.5">
            {collapsed ? (
              <div className="mx-2 my-1 border-t border-neutral/60" />
            ) : (
              <p className="px-2.5 pb-1 pt-2 text-[10px] font-semibold uppercase tracking-wider text-base-content/65">
                {section.title}
              </p>
            )}
            {section.items.map(({ href, label, icon: Icon }) => {
              const active = pathname === href || pathname.startsWith(href + "/");
              return (
                <Link
                  key={href}
                  href={href}
                  title={collapsed ? label : undefined}
                  aria-label={label}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm transition-colors",
                    active
                      ? "bg-primary font-medium text-primary-content"
                      : "text-base-content/65 hover:bg-base-300 hover:text-base-content",
                  )}
                >
                  <Icon className="h-4 w-4 shrink-0" aria-hidden />
                  {!collapsed && <span className="truncate">{label}</span>}
                </Link>
              );
            })}
          </div>
        ))}
        <SidebarAuth collapsed={collapsed} />
      </nav>

      <div className="border-t border-neutral p-2">
        <ProjectSwitcher collapsed={collapsed} />
        <button
          hidden={mobile}
          onClick={toggle}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          className="flex w-full items-center justify-center rounded-lg py-1.5 text-base-content/50 hover:bg-base-300 hover:text-base-content"
        >
          {collapsed ? (
            <ChevronsRight className="h-4 w-4" />
          ) : (
            <ChevronsLeft className="h-4 w-4" />
          )}
        </button>
        {!collapsed && (
          <p className="mt-1 flex items-center justify-center gap-1 text-center text-[10px] text-base-content/35">
            <Activity className="h-3 w-3" aria-hidden /> connected observability
          </p>
        )}
      </div>
    </aside>
  );
}
