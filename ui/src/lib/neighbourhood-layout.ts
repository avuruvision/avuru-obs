import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import { ROLE_PEER } from "@/lib/map-peers";

// Geometry for the service detail page's neighbourhood diagram: who calls this
// service, and what it calls, in three fixed columns.
//
// Pure and renderer-free, the way lib/map-filter.ts is cytoscape-free — this is
// what decides what the reader sees, and it should be readable without knowing
// how the arrows are drawn. It is also why the layout is DETERMINISTIC rather
// than simulated: the whole claim of this picture is that left means "calls
// me" and right means "I call it", which a force layout would erase.

export const CARD_W = 190;
export const CARD_H = 66;
// Wider than the trace path's gap. An edge here carries up to two label lines
// ("4.0/min · 40ms · 10.0%", then "via <proxy>"), the label is centred in the
// gap, and it must clear the cards on both sides rather than run under one.
const COL_GAP = 180;
const ROW_GAP = 22;
const PAD = 28;

// Peers drawn per side before the column stops and says how many are left.
// Eight is about what fits before the cards are too small to read; the table
// view remains the complete list, which is why truncating here is honest.
export const MAX_PER_SIDE = 8;

export type NodeKind =
  // The service this page is about.
  | "centre"
  // A real service: it sent telemetry of its own, so its numbers are its own.
  | "service"
  // A derived dependency or an unresolved peer: every number about it was
  // measured somewhere else, so it carries none.
  | "derived"
  // "+N more" — the overflow marker, not a workload.
  | "more"
  // "nothing here" — the placeholder that keeps an empty side from collapsing.
  | "empty";

export interface PlacedNode {
  /** Column-scoped, because a service can be on BOTH sides of a cycle. */
  id: string;
  /** Headline text. For `more`/`empty` this is the message itself. */
  name: string;
  kind: NodeKind;
  x: number;
  y: number;
  /** The hub's node for this service. Absent means nothing measured it. */
  stats?: ServiceStats;
  /** Shown instead of metrics when there are none to show. */
  note?: string;
  /** Hover text. */
  title?: string;
  clickable: boolean;
}

export interface PlacedLink {
  key: string;
  edge: ServiceEdge;
  from: PlacedNode;
  to: PlacedNode;
}

export interface Neighbourhood {
  nodes: PlacedNode[];
  links: PlacedLink[];
  width: number;
  height: number;
}

// Both directions of a service's neighbourhood, most traffic first.
//
// The diagram and the tables MUST agree about which peer comes first, so the
// split lives here and both views read it rather than each filtering the edge
// set their own way.
export function splitNeighbourhood(
  service: string,
  edges: ServiceEdge[],
): { upstream: ServiceEdge[]; downstream: ServiceEdge[] } {
  const byCalls = (a: ServiceEdge, b: ServiceEdge) => b.calls - a.calls;
  return {
    upstream: edges.filter((e) => e.target === service).sort(byCalls),
    downstream: edges.filter((e) => e.source === service).sort(byCalls),
  };
}

// A peer with no node in the map's service list was never observed directly —
// the hub saw an edge pointing at it and nothing else. It gets a card, because
// deleting the connection would hide exactly the parts of the estate nobody has
// instrumented yet (lib/map-peers.ts), but it gets no numbers.
//
// Deliberately not withUndetectedPeers(): that helper zeroes every metric so
// cytoscape has something to size a node by, and "0/min · 0ms" is the precise
// lie this page's design forbids. Absent has to stay absent.
function describe(
  side: "up" | "down",
  name: string,
  stats: ServiceStats | undefined,
): PlacedNode {
  // A service can call this one AND be called by it. That is a cycle, both
  // cards are true, and the id has to keep them apart.
  const id = `${side}:${name}`;
  if (!stats || stats.role === ROLE_PEER) {
    return {
      id,
      name,
      kind: "derived",
      x: 0,
      y: 0,
      note: "no telemetry of its own",
      title: `${name} — seen only as the far end of a connection. It sends no telemetry, so every number about it would be its caller's.`,
      clickable: false,
    };
  }
  if (stats.role === "virtual") {
    return {
      id,
      name,
      kind: "derived",
      x: 0,
      y: 0,
      stats,
      note: `${stats.kind ?? "dependency"} · measured at the caller`,
      title: `${name} — a derived dependency: it sends no telemetry, so everything shown about it comes from the exit spans of the services calling it.`,
      clickable: false,
    };
  }
  return {
    id,
    name,
    kind: "service",
    x: 0,
    y: 0,
    stats,
    clickable: true,
  };
}

function overflow(side: "up" | "down", hidden: number): PlacedNode {
  return {
    id: `${side}-more`,
    name: `+${hidden} more`,
    kind: "more",
    x: 0,
    y: 0,
    note: "switch to the table for all of them",
    title: `${hidden} more ${side === "up" ? "callers" : "dependencies"} than the diagram draws. The table view lists every one.`,
    clickable: false,
  };
}

function placeholder(side: "up" | "down"): PlacedNode {
  return side === "up"
    ? {
        id: "up-empty",
        name: "No callers observed",
        kind: "empty",
        x: 0,
        y: 0,
        note: "an entry point, or callers not instrumented",
        title:
          "Nothing was observed calling this service in this window. It may be an entry point, or its callers may not be instrumented.",
        clickable: false,
      }
    : {
        id: "down-empty",
        name: "No outgoing calls",
        kind: "empty",
        x: 0,
        y: 0,
        note: "nothing observed leaving this service",
        title: "No outgoing calls observed in this window.",
        clickable: false,
      };
}

// Lays the neighbourhood out as callers | this service | dependencies.
export function buildNeighbourhood({
  service,
  services,
  upstream,
  downstream,
}: {
  service: string;
  services: ServiceStats[];
  upstream: ServiceEdge[];
  downstream: ServiceEdge[];
}): Neighbourhood {
  const byName = new Map(services.map((s) => [s.name, s]));

  const build = (side: "up" | "down", edges: ServiceEdge[]) => {
    const shown = edges.slice(0, MAX_PER_SIDE);
    const cards = shown.map((e) => {
      const peer = side === "up" ? e.source : e.target;
      return describe(side, peer, byName.get(peer));
    });
    if (edges.length > shown.length) cards.push(overflow(side, edges.length - shown.length));
    if (cards.length === 0) cards.push(placeholder(side));
    return { cards, edges: shown };
  };

  const up = build("up", upstream);
  const down = build("down", downstream);

  const centreStats = byName.get(service);
  const centre: PlacedNode = {
    id: service,
    name: service,
    kind: "centre",
    x: 0,
    y: 0,
    stats: centreStats,
    // Not clickable: this IS the page it would open.
    clickable: false,
  };

  const columns = [up.cards, [centre], down.cards];
  const tallest = Math.max(...columns.map((c) => c.length));
  columns.forEach((cards, col) => {
    // Each column is centred against the tallest, so a service with one caller
    // and six dependencies does not hang off the top edge.
    const offset = ((tallest - cards.length) * (CARD_H + ROW_GAP)) / 2;
    const x = PAD + col * (CARD_W + COL_GAP);
    cards.forEach((card, row) => {
      card.x = x;
      card.y = PAD + offset + row * (CARD_H + ROW_GAP);
    });
  });

  const links: PlacedLink[] = [];
  // Labels sit at the middle of the column gap (see the renderer), which is the
  // only place equidistant from both cards. Every edge on one side shares that
  // x, so they separate by row instead of by distance along the curve.
  up.edges.forEach((edge, i) => {
    const from = up.cards[i];
    if (from) links.push({ key: `${edge.source}->${edge.target}`, edge, from, to: centre });
  });
  down.edges.forEach((edge, i) => {
    const to = down.cards[i];
    if (to) links.push({ key: `${edge.source}->${edge.target}`, edge, from: centre, to });
  });

  return {
    nodes: [...up.cards, centre, ...down.cards],
    links,
    width: PAD * 2 + 3 * (CARD_W + COL_GAP) - COL_GAP,
    height: PAD * 2 + tallest * (CARD_H + ROW_GAP) - ROW_GAP,
  };
}
