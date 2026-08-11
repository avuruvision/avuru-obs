import type { Core } from "cytoscape";

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
// carbon=false must leave the graph byte-identical to a non-green install.
export function applyStyle(cy: Core, carbon = false, compact = false) {
  const c = themeColors();
  const s = scale(compact);

  const withNodes = cy
    .style()
    .resetToDefault()
    .selector("node")
    .style({
      "background-color": c.primary,
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
    .style({ "border-color": c.error });

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

  withCarbon
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
    // Network-health amber (high RTT or failed connections) — before the error
    // selector so trace-error red always wins when an edge is both.
    .selector("edge[health > 0]")
    .style({ "line-color": c.warning, "target-arrow-color": c.warning, "line-style": "dashed" })
    .selector("edge[error > 0]")
    .style({ "line-color": c.error, "target-arrow-color": c.error, "line-style": "solid" })
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
