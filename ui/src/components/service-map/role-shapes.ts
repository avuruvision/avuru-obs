import type { Css } from "cytoscape";

// How each mesh role is drawn, and how that drawing is named in a legend.
//
// Shape is the channel that carries WHAT A NODE IS (graph-style.ts), so a role
// is a shape and never a colour: the six colour channels are spoken for, and a
// waypoint painted amber would read as degraded. Every entry below is a
// variation on the transport diamond's neutral fill — the fill still says
// "proxy", the outline says which kind.
//
// The wire values are the hub's (mesh/mesh-roles.ts owns their English names);
// a role with no entry here draws as the plain diamond, which is the honest
// shape for a proxy we cannot place.
//
//   control-plane    star             it steers the rest, and carries nothing
//   ingress-gateway  tag              a pentagon with a point: the door in
//   egress-gateway   reversed tag     the same door, pointing out
//   gateway          pentagon         a door whose direction the hub did not say
//   waypoint         double ring      the diamond, with the L7 layer drawn on it
//   ztunnel          chevron (vee)    the per-node tunnel traffic passes through
//   sidecar          leaning diamond  the diamond pushed beside its pod
//
// The waypoint keeps the diamond and spends border STYLE rather than shape —
// border colour stays health's, and "double" is unused elsewhere (dashed
// already means "inferred"). Width 4 rather than the base 3 so the two lines
// read as two at overview scale.
export interface RoleShape {
  style: Css.Node;
  // The word the legend puts before "= Waypoint": what the reader sees.
  legend: string;
}

// cytoscape's `tag` is [-1,-1 0.25,-1 1,0 0.25,1 -1,1]; this is its mirror.
const REVERSED_TAG = "1 -1 -0.25 -1 -1 0 -0.25 1 1 1";

export const MESH_ROLE_SHAPES: Record<string, RoleShape> = {
  "control-plane": { style: { shape: "star" }, legend: "star" },
  "ingress-gateway": { style: { shape: "tag" }, legend: "tag" },
  "egress-gateway": {
    style: { shape: "polygon", "shape-polygon-points": REVERSED_TAG },
    legend: "reversed tag",
  },
  gateway: { style: { shape: "pentagon" }, legend: "pentagon" },
  waypoint: {
    style: { "border-style": "double", "border-width": 4 },
    legend: "double-ringed diamond",
  },
  ztunnel: { style: { shape: "vee" }, legend: "chevron" },
  sidecar: { style: { shape: "rhomboid" }, legend: "leaning diamond" },
};

// The legend word for a role, including one this table does not know: such a
// node is drawn as the base diamond, and saying so is truer than saying nothing.
export function roleShapeWord(role: string): string {
  return MESH_ROLE_SHAPES[role]?.legend ?? "diamond";
}
