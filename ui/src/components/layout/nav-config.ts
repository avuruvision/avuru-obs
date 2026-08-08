import {
  Activity,
  Boxes,
  BellRing,
  Bug,
  Flame,
  Gauge,
  LayoutDashboard,
  Leaf,
  ListTree,
  Map as MapIcon,
  ScrollText,
  Server,
  Settings,
  type LucideIcon,
} from "lucide-react";
import type { ModuleName } from "@/lib/api-types";

// The navigation model: grouped sections (Kiali-style IA). One source of truth
// for the sidebar AND breadcrumbs (the avuru-obs analog of Kiali's routes).
export interface NavItem {
  href: string;
  label: string;
  icon: LucideIcon;
  // Module owning this entry; omitted = core (always shown). The sidebar
  // hides entries whose module is inactive — see useCapabilities.
  module?: ModuleName;
}

export interface NavSection {
  title: string;
  items: NavItem[];
}

export const NAV_SECTIONS: NavSection[] = [
  {
    title: "Overview",
    // Core, and the landing route — the Dashboard's own bands gate per module,
    // so this entry is never hidden.
    items: [{ href: "/dashboard", label: "Dashboard", icon: LayoutDashboard }],
  },
  {
    title: "Observe",
    items: [
      { href: "/service-map", label: "Service Map", icon: MapIcon },
      { href: "/services", label: "Services", icon: Boxes },
      { href: "/health", label: "Service Health", icon: Activity, module: "service-health" },
      { href: "/traces", label: "Traces", icon: ListTree },
      { href: "/logs", label: "Logs", icon: ScrollText, module: "logs" },
      // RED is derived from traces, not from the metrics tables — core.
      { href: "/metrics", label: "Metrics", icon: Gauge },
      { href: "/profiling", label: "Profiling", icon: Flame, module: "profiling" },
      { href: "/errors", label: "Errors", icon: Bug, module: "error-tracking" },
      { href: "/alerts", label: "Alerts", icon: BellRing, module: "alerting" },
      { href: "/green", label: "Green", icon: Leaf, module: "green" },
    ],
  },
  {
    title: "Infrastructure",
    items: [{ href: "/nodes", label: "Nodes", icon: Server, module: "infra-metrics" }],
  },
  {
    title: "System",
    items: [{ href: "/settings", label: "Settings", icon: Settings }],
  },
];

const ALL_ITEMS = NAV_SECTIONS.flatMap((s) =>
  s.items.map((i) => ({ ...i, section: s.title })),
);

// Resolve the current route's label + section from a pathname (for breadcrumbs).
export function routeInfo(
  pathname: string,
): { label: string; section: string } | undefined {
  return ALL_ITEMS.find(
    (i) => pathname === i.href || pathname.startsWith(i.href + "/"),
  );
}
