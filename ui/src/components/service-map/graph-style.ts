import type { Core } from "cytoscape";
import { MESH_ROLE_SHAPES } from "./role-shapes";

// Resolve Avuru Gold tokens from the live theme (daisyUI CSS vars) so the graph
// follows light/dark — never hardcode hex (agent_docs/ui_patterns.md).
export function themeColors() {
  const cs = getComputedStyle(document.documentElement);
  const v = (name: string, fallback: string) =>
    cs.getPropertyValue(name).trim() || fallback;
  return {
    primary: v("--color-primary", "#c9a96a"),
    error: v("--color-error", "#f87171"),
    warning: v("--color-warning", "#f59e0b"),
    success: v("--color-success", "#34d399"),
    base100: v("--color-base-100", "#0b1120"),
    text: v("--color-base-content", "#e8e5dc"),
    neutral: v("--color-neutral", "#33415580"),
  };
}

// Compact mode (the Dashboard's band 2) is the SAME graph at overview scale.
const scale = (compact: boolean) => ({
  node: compact ? "mapData(rate, 0, 10, 14, 40)" : "mapData(rate, 0, 10, 22, 64)",
  // A barrel drawn square is just a rounded box. Narrowing it to ~0.7 of the
  // node width — height untouched, so size still means rate — is what gives it
  // the portrait proportions a datastore glyph is read by.
  barrelWidth: compact ? "mapData(rate, 0, 10, 10, 28)" : "mapData(rate, 0, 10, 15, 45)",
  fontSize: compact ? 9 : 11,
  labelMargin: compact ? 3 : 5,
  edge: compact ? "mapData(calls, 0, 50, 0.8, 3.2)" : "mapData(calls, 0, 50, 1.2, 5)",
});

// applyStyle rebuilds the whole stylesheet. Channels, and why each one:
//
//   ring (border)  health status — ALWAYS on, so it must own a stable channel
//   fill           service identity (primary)
//   size           request rate
//   halo (underlay) gCO2e, carbon lens only — moved off the border in v0.5 W7
//                  so the status ring could have it; the lens reads as an
//                  overlay rather than a repaint
//   width          call volume
//   line color     plain / amber (network health) / red (trace errors)
//
// Four treatments sit outside that set and must not disturb it. Three are node
// SHAPES, so that the primary-filled hexagon keeps meaning "application":
// a transport node (mesh proxy / gateway, hidden unless the user asks for it)
// is a diamond in the neutral tone, a virtual target (database / cache /
// broker) a dashed barrel, and a peer the renderer could not resolve a hollow
// outline. The fourth is on edges: a flow-only edge is DOTTED, because dashed
// is already spoken for by network health.
//
// Boundary boxes (compound parents, below) are a fifth thing again — they are
// containers, not nodes, and they take no pointer events.
//
// Node shape carries WHAT A NODE IS. It is a separate channel from the six
// above precisely so that adding a kind of node never costs the map a colour.
//
// carbon=false must leave the graph byte-identical to a non-green install.
export function applyStyle(cy: Core, carbon = false, compact = false, edgeLabels = false) {
  const c = themeColors();
  const s = scale(compact);

  const withNodes = cy
    .style()
    .resetToDefault()
    .selector("node")
    .style({
      "background-color": c.primary,
      // Application: the unmarked default. Every other node kind overrides
      // this below, so a hexagon is what a node IS until something more
      // specific is known about it. Explicit rather than inherited —
      // cytoscape's own default is an ellipse, and a shape this load-bearing
      // should not be a library default nobody wrote down.
      shape: "round-hexagon",
      label: "data(label)",
      color: c.text,
      "font-size": s.fontSize,
      "text-valign": "bottom",
      "text-margin-y": s.labelMargin,
      // Keep labels legible where they sit over edges/nodes.
      "text-background-color": c.base100,
      "text-background-opacity": 0.7,
      "text-background-padding": "2px",
      "text-background-shape": "roundrectangle",
      // wrap so the focus label's second line breaks on its \n.
      "text-wrap": "wrap",
      width: s.node,
      height: s.node,
      "border-width": 3,
      // Default ring: unknown/idle. Never green — an unmeasured service must
      // not read as healthy.
      //
      // These four colors are the daisyUI tokens that lib/health-status.ts's
      // `bg-success`/`bg-warning`/`bg-error`/`bg-base-content/30` resolve to.
      // Do NOT import statusDotClass here — it returns Tailwind class names,
      // which cytoscape cannot use; the shared thing is the token, not the code.
      "border-color": c.neutral,
      "transition-property": "opacity, border-width",
      "transition-duration": 120,
    })
    // Status rings. Colors follow lib/health-status.ts so the map and the
    // health board cannot disagree about what amber means.
    .selector('node[status = "healthy"]')
    .style({ "border-color": c.success })
    .selector('node[status = "degraded"]')
    .style({ "border-color": c.warning })
    .selector('node[status = "down"]')
    .style({ "border-color": c.error })
    // Module-off fallback ring (see graph-elements.ts). Only nodes on a
    // health-less install carry `errorRing`, so this is inert everywhere else.
    .selector("node[errorRing > 0]")
    .style({ "border-color": c.error })
    // Transport (mesh sidecar / waypoint / ingress gateway). Shape and fill,
    // not the ring — the ring is health's channel and a proxy has health too.
    .selector("node[transport > 0]")
    .style({ shape: "round-diamond", "background-color": c.neutral, "background-opacity": 0.85 });

  // Per-role transport shapes (role-shapes.ts owns the table and the why).
  // After the generic transport rule so each one overrides only what it names
  // — shape, or for the waypoint the border style — and inherits the neutral
  // fill. Only nodes buildElements stamped with `meshRole` match, and it stamps
  // nothing when the role is unknown, so the map without roles is untouched.
  for (const [role, { style }] of Object.entries(MESH_ROLE_SHAPES)) {
    withNodes.selector(`node[transport > 0][meshRole = "${role}"]`).style(style);
  }

  withNodes
    // Virtual target (database / cache / broker). Same rule as transport:
    // SHAPE and fill, never the ring. A barrel drawn PORTRAIT is the nearest
    // thing in cytoscape's shape set to the cylinder that means "datastore"
    // everywhere — it bows its sides but has no elliptical cap, so it is a
    // gesture at the glyph rather than the glyph. What it does do is stay
    // distinguishable from both the hexagon and the diamond at overview scale,
    // which a rounded square is not. The dashed border says "inferred" — this
    // node was derived from its callers' spans, not observed sending anything. It sets no border COLOR:
    // that leaves the neutral base ring (a virtual target has no health verdict
    // to show) while still letting the module-off error ring above come
    // through, so a database failing every call reads red on a health-less
    // install.
    .selector("node[virtual > 0]")
    .style({
      shape: "barrel",
      width: s.barrelWidth,
      "background-color": c.neutral,
      "background-opacity": 0.85,
      "border-style": "dashed",
      "border-width": 2,
    })
    // A peer the renderer could not resolve to a service: something the sensor
    // saw traffic to that never sent telemetry of its own. Drawn as an OUTLINE
    // — hollow, because there is nothing inside it we know. It keeps the
    // hexagon, since it probably is an ordinary workload; what it lacks is a
    // voice, not a category.
    .selector("node[peer > 0]")
    .style({
      "background-opacity": 0,
      "border-color": c.neutral,
      "border-style": "dashed",
      "border-width": 2,
      "text-opacity": 0.6,
    });

  // Boundary boxes (compound parents: namespace or health group). Drawn as a
  // faint dashed container with its name at the top — deliberately quiet, since
  // a boundary is context for the nodes, not a thing competing with them for
  // attention. Sized by its children, so no width/height here.
  withNodes
    .selector(":parent")
    .style({
      shape: "round-rectangle",
      "background-color": c.neutral,
      "background-opacity": 0.12,
      "border-color": c.neutral,
      "border-width": 1,
      "border-style": "dashed",
      label: "data(label)",
      color: c.text,
      "font-size": s.fontSize + (compact ? 0 : 1),
      // Label INSIDE the box, not above it. Above collides with whatever the
      // layout puts overhead — and with a second boundary directly above, the
      // two names land on each other. The padding a compound already reserves
      // around its children is empty space the label can live in.
      "text-valign": "top",
      "text-halign": "center",
      "text-margin-y": compact ? 13 : 21,
      "text-background-opacity": 0,
      "text-opacity": 0.6,
      padding: compact ? "12px" : "22px",
      // A box must never eat a hover meant for the node inside it, and it has
      // no traces to open — so it takes no pointer events at all.
      events: "no",
    });

  // Carbon halo (low→high gCO2e). Applied after the ring selectors so a node
  // can read status (ring) and carbon (halo) at once. Only bucketed nodes carry
  // the `carbon` attribute, so nodes without energy get no halo.
  const halo = (color: string) => ({
    "underlay-color": color,
    "underlay-opacity": 0.25,
    "underlay-padding": compact ? 4 : 7,
  });
  const withCarbon = carbon
    ? withNodes
        .selector("node[carbon = 0]")
        .style(halo(c.success))
        .selector("node[carbon = 1]")
        .style(halo(c.warning))
        .selector("node[carbon = 2]")
        .style(halo(c.error))
    : withNodes;

  const withEdges = withCarbon
    .selector("edge")
    .style({
      width: s.edge,
      "line-color": c.neutral,
      "target-arrow-color": c.neutral,
      "target-arrow-shape": "triangle",
      "arrow-scale": 0.9,
      "curve-style": "bezier",
      opacity: 0.85,
      "transition-property": "opacity, width",
      "transition-duration": 120,
    })
    // Flow-only: a connection the sensor observed with no traced call behind
    // it. First, so an edge that is ALSO unhealthy or errored still gets the
    // dashed amber / solid red it needs — urgency outranks provenance.
    .selector("edge[flow > 0]")
    .style({ "line-style": "dotted", opacity: 0.6 })
    // Network-health amber (high RTT or failed connections) — before the error
    // selector so trace-error red always wins when an edge is both.
    .selector("edge[health > 0]")
    .style({ "line-color": c.warning, "target-arrow-color": c.warning, "line-style": "dashed" })
    .selector("edge[error > 0]")
    .style({ "line-color": c.error, "target-arrow-color": c.error, "line-style": "solid" });

  // Always-on edge volume. Off by default: on a dense graph every label is a
  // label too many, and the hover already answers the single-edge question.
  // When asked for it shows volume ONLY — the focus label still owns latency
  // and errors, so the two do not fight for the same line.
  const withEdgeLabels = edgeLabels
    ? withEdges.selector("edge").style({
        label: "data(volumeLabel)",
        "font-size": s.fontSize - 1,
        color: c.text,
        "text-opacity": 0.75,
        "text-background-color": c.base100,
        "text-background-opacity": 0.7,
        "text-background-padding": "2px",
        "text-background-shape": "roundrectangle",
        "text-rotation": "autorotate",
      })
    : withEdges;

  withEdgeLabels
    // ---- Hover focus ----
    // Everything outside the focused neighbourhood recedes.
    .selector(".faded")
    .style({ opacity: 0.18, "text-opacity": 0.18 })
    // The hovered node: thicker ring and the expanded two-line label.
    .selector("node.focus")
    .style({ "border-width": 5, label: "data(focusLabel)", "font-size": s.fontSize + 1 })
    // Its edges: thicker, fully opaque, labelled with rpm/latency, and carrying
    // a mid-line arrowhead so direction reads without following the line to its
    // end. NOT an animated dash — dashed already means "network-unhealthy".
    .selector("edge.related")
    .style({
      opacity: 1,
      width: compact ? 3 : 5,
      label: "data(focusLabel)",
      "font-size": s.fontSize,
      color: c.text,
      "text-background-color": c.base100,
      "text-background-opacity": 0.85,
      "text-background-padding": "2px",
      "text-background-shape": "roundrectangle",
      "text-rotation": "autorotate",
      "mid-target-arrow-shape": "triangle",
      "mid-target-arrow-color": c.neutral,
      "arrow-scale": 1.1,
    })
    // The .related selector above sets a neutral mid-arrow unconditionally;
    // these run after it in the cascade to correct a focused edge that is
    // itself network-unhealthy (amber) or errored (red), so the mid-arrow
    // doesn't contradict the line color it sits on.
    .selector("edge.related[health > 0]")
    .style({ "mid-target-arrow-color": c.warning })
    .selector("edge.related[error > 0]")
    .style({ "mid-target-arrow-color": c.error })
    .update();
}
