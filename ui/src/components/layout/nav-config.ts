import {
  Boxes,
  Flame,
  Gauge,
  ListTree,
  Map as MapIcon,
  ScrollText,
  Server,
  Settings,
  type LucideIcon,
} from "lucide-react";

// The navigation model: grouped sections (Kiali-style IA). One source of truth
// for the sidebar AND breadcrumbs (the avuru-obs analog of Kiali's routes).
export interface NavItem {
  href: string;
  label: string;
  icon: LucideIcon;
}

export interface NavSection {
  title: string;
  items: NavItem[];
}

export const NAV_SECTIONS: NavSection[] = [
  {
    title: "Observe",
    items: [
      { href: "/service-map", label: "Service Map", icon: MapIcon },
      { href: "/services", label: "Services", icon: Boxes },
      { href: "/traces", label: "Traces", icon: ListTree },
      { href: "/logs", label: "Logs", icon: ScrollText },
      { href: "/metrics", label: "Metrics", icon: Gauge },
      { href: "/profiling", label: "Profiling", icon: Flame },
    ],
  },
  {
    title: "Infrastructure",
    items: [{ href: "/nodes", label: "Nodes", icon: Server }],
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
