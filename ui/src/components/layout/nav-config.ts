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
  // Path on the docs site that explains this screen, without a leading slash.
  // Omitted where no page covers it yet: a link that 404s is worse than no
  // link, so the header simply shows nothing rather than guessing.
  docs?: string;
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
    items: [{ href: "/dashboard", label: "Dashboard", icon: LayoutDashboard, docs: "getting-started/core-concepts" }],
  },
  {
    title: "Observe",
    items: [
      { href: "/service-map", label: "Service Map", icon: MapIcon, docs: "getting-started/core-concepts" },
      { href: "/services", label: "Services", icon: Boxes, docs: "signals/metrics" },
      { href: "/health", label: "Service Health", icon: Activity, module: "service-health", docs: "signals/service-health" },
      { href: "/traces", label: "Traces", icon: ListTree, docs: "signals/traces" },
      { href: "/logs", label: "Logs", icon: ScrollText, module: "logs", docs: "signals/logs" },
      // RED is derived from traces, not from the metrics tables — core.
      { href: "/metrics", label: "Metrics", icon: Gauge, docs: "signals/metrics" },
      { href: "/profiling", label: "Profiling", icon: Flame, module: "profiling", docs: "signals/profiling" },
      { href: "/errors", label: "Errors", icon: Bug, module: "error-tracking", docs: "signals/errors" },
      { href: "/alerts", label: "Alerts", icon: BellRing, module: "alerting", docs: "signals/alerting" },
      { href: "/green", label: "Green", icon: Leaf, module: "green", docs: "signals/green" },
    ],
  },
  {
    title: "Infrastructure",
    items: [{ href: "/nodes", label: "Nodes", icon: Server, module: "infra-metrics", docs: "signals/metrics" }],
  },
  {
    title: "System",
    items: [{ href: "/settings", label: "Settings", icon: Settings, docs: "setup/modules" }],
  },
];

const ALL_ITEMS = NAV_SECTIONS.flatMap((s) =>
  s.items.map((i) => ({ ...i, section: s.title })),
);

// Resolve the current route's label + section from a pathname (for breadcrumbs).
export function routeInfo(
  pathname: string,
): { label: string; section: string; docs?: string } | undefined {
  return ALL_ITEMS.find(
    (i) => pathname === i.href || pathname.startsWith(i.href + "/"),
  );
}

// Where the docs live. A constant rather than a build flag: the docs site is
// versionless and public, and an install that cannot reach the internet simply
// has a link that does not resolve — which is no worse than the help it never
// had. Anything self-hosted is a fork of this line.
export const DOCS_BASE = "https://avuruobs.io/docs";

// docsUrl is the full link for a route, or undefined when no page covers it.
export function docsUrl(pathname: string): string | undefined {
  const info = routeInfo(pathname);
  return info?.docs ? `${DOCS_BASE}/${info.docs}` : undefined;
}
