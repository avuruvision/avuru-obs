import type { MeshProxy } from "@/lib/api-types";

// How each role reads on screen. The hub sends the wire value; this is the only
// place that turns it into English, so the table, the facet and the detail view
// cannot disagree about what a "control-plane" is called.
//
// A role this map does not know renders verbatim rather than as "Unknown": a
// hub newer than the UI is a normal state during a rolling upgrade, and showing
// the raw value tells the reader something true.
const ROLE_LABELS: Record<string, string> = {
  "control-plane": "Control plane",
  "ingress-gateway": "Ingress gateway",
  "egress-gateway": "Egress gateway",
  gateway: "Gateway",
  waypoint: "Waypoint",
  ztunnel: "ztunnel",
  sidecar: "Sidecar",
};

// Display order for the facet: the path traffic takes through a mesh, from the
// edge inward, with the control plane last because it carries none of it.
const ROLE_ORDER = [
  "ingress-gateway",
  "gateway",
  "waypoint",
  "ztunnel",
  "sidecar",
  "egress-gateway",
  "control-plane",
];

export function roleLabel(role: string): string {
  return ROLE_LABELS[role] ?? role;
}

// The roles actually present, in ROLE_ORDER, with anything unrecognised after
// them. Deriving from the rows in scope rather than from ROLE_ORDER keeps the
// facet from offering choices that would match nothing.
export function rolesPresent(proxies: MeshProxy[]): string[] {
  const seen = new Set(proxies.map((p) => p.role).filter((r): r is string => !!r));
  const known = ROLE_ORDER.filter((r) => seen.has(r));
  const unknown = [...seen].filter((r) => !ROLE_ORDER.includes(r)).sort();
  return [...known, ...unknown];
}

export function namespacesPresent(proxies: MeshProxy[]): string[] {
  return [...new Set(proxies.map((p) => p.namespace).filter((n): n is string => !!n))].sort();
}
