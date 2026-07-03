"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Activity, ChevronsLeft, ChevronsRight, Hexagon } from "lucide-react";
import { cn } from "@/lib/cn";
import { useLocalStorageFlag } from "@/hooks/use-local-storage-flag";
import { NAV_SECTIONS } from "./nav-config";

const COLLAPSE_KEY = "avuru-sidebar-collapsed";

// Coroot layout: fixed left nav, collapsible with localStorage persistence
// (hydration-safe via useSyncExternalStore — see use-local-storage-flag).
export function Sidebar() {
  const pathname = usePathname();
  const [collapsed, setCollapsed] = useLocalStorageFlag(COLLAPSE_KEY);
  const toggle = () => setCollapsed(!collapsed);

  return (
    <aside
      className={cn(
        "flex h-screen shrink-0 flex-col border-r border-neutral bg-base-200 transition-[width] duration-150",
        collapsed ? "w-14" : "w-52",
      )}
    >
      <Link
        href="/traces"
        className="flex h-14 items-center gap-2 border-b border-neutral px-4"
      >
        <Hexagon className="h-5 w-5 shrink-0 text-primary" aria-hidden />
        {!collapsed && (
          <span className="text-sm font-bold tracking-tight">avuru obs</span>
        )}
      </Link>

      <nav aria-label="Primary" className="flex flex-1 flex-col gap-1 overflow-y-auto p-2">
        {NAV_SECTIONS.map((section) => (
          <div key={section.title} className="flex flex-col gap-0.5">
            {collapsed ? (
              <div className="mx-2 my-1 border-t border-neutral/60" />
            ) : (
              <p className="px-2.5 pb-1 pt-2 text-[10px] font-semibold uppercase tracking-wider text-base-content/40">
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
                  className={cn(
                    "flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm transition-colors",
                    active
                      ? "bg-primary/10 font-semibold text-primary"
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
      </nav>

      <div className="border-t border-neutral p-2">
        <button
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
            <Activity className="h-3 w-3" aria-hidden /> live in 5 minutes
          </p>
        )}
      </div>
    </aside>
  );
}
