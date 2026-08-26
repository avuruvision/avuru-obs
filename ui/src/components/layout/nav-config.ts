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
  Wallet,
  Waypoints,
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

// Sections are LAYERS, each answering a different question, and they are
// ordered the way an investigation runs: what is out there → what is it doing →
// what needs me → what it runs on.
//
// Until v0.8 nine of the thirteen entries sat under one "Observe" heading,
// which is a list, not a structure: it grew with every module and told the
// reader nothing about how the screens relate. The wedge's first-five-minutes
// path is unaffected — Service Map is still the first link under the landing
// route, one click from anywhere.
//
// A section whose every entry belongs to an inactive module renders nothing at
// all (see Sidebar), so this shape does not put empty headings on a minimal
// install.
export const NAV_SECTIONS: NavSection[] = [
  {
    title: "Overview",
    // Core, and the landing route — the Dashboard's own bands gate per module,
    // so this entry is never hidden.
    items: [{ href: "/dashboard", label: "Dashboard", icon: LayoutDashboard, docs: "getting-started/core-concepts" }],
  },
  {
    // "What is out there, and is it well?" — the inventory layer. The map
    // leads it, because on a fresh install it is the first thing that fills in.
    //
    // NOT titled "Services": a section named exactly like one of its own
    // entries is ambiguous to read and to click. "Topology" is already the
    // product's word for this view — it titles the Dashboard's map card.
    title: "Topology",
    items: [
      { href: "/service-map", label: "Service Map", icon: MapIcon, docs: "signals/service-map" },
      { href: "/services", label: "Services", icon: Boxes, docs: "signals/metrics" },
      { href: "/health", label: "Service Health", icon: Activity, module: "service-health", docs: "signals/service-health" },
    ],
  },
  {
    // "What happened?" — the raw signals, in the order you reach for them.
    title: "Signals",
    items: [
      { href: "/traces", label: "Traces", icon: ListTree, docs: "signals/traces" },
      { href: "/logs", label: "Logs", icon: ScrollText, module: "logs", docs: "signals/logs" },
      // RED is derived from traces, not from the metrics tables — core.
      { href: "/metrics", label: "Metrics", icon: Gauge, docs: "signals/metrics" },
      { href: "/profiling", label: "Profiling", icon: Flame, module: "profiling", docs: "signals/profiling" },
    ],
  },
  {
    // "What needs me?" — the two surfaces that exist to demand attention.
    title: "Operations",
    items: [
      { href: "/errors", label: "Errors", icon: Bug, module: "error-tracking", docs: "signals/errors" },
      { href: "/alerts", label: "Alerts", icon: BellRing, module: "alerting", docs: "signals/alerting" },
    ],
  },
  {
    // "What does it run on, and what does that cost?" Green sits here rather
    // than beside the signals because energy and carbon are properties of the
    // fleet, not another telemetry stream — and it is where the cost surface
    // joined it.
    title: "Infrastructure",
    items: [
      { href: "/nodes", label: "Nodes", icon: Server, module: "infra-metrics", docs: "signals/metrics" },
      // The mesh sits here rather than under Topology: the map's subject is
      // what your services depend on, and the mesh is what they run ON — the
      // same reason Nodes and Green are neighbours. Hidden entirely without the
      // module, which is most installs.
      { href: "/mesh", label: "Mesh", icon: Waypoints, module: "mesh", docs: "signals/mesh" },
      { href: "/green", label: "Green", icon: Leaf, module: "green", docs: "signals/green" },
      // Cost is Green's nearest neighbour, not a separate concern: one measures
      // the energy a workload draws, the other the capacity nobody drew on.
      { href: "/cost", label: "Cost", icon: Wallet, module: "cost", docs: "signals/cost" },
    ],
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
