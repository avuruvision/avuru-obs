import type { Core, NodeSingular } from "cytoscape";

const CLASSES = "focus related faded";

// Hover focus: the hovered node and its 1-hop neighbourhood stay lit, its edges
// thicken and reveal their rpm/latency labels, and everything else recedes.
// Classes only — the layout must NOT move, so nothing is added or removed.
export function focusNeighbourhood(cy: Core, node: NodeSingular) {
  const edges = node.connectedEdges();
  const keep = edges.connectedNodes().union(node);
  cy.elements().removeClass(CLASSES);
  cy.elements().difference(keep.union(edges)).addClass("faded");
  edges.addClass("related");
  node.addClass("focus");
}

export function clearFocus(cy: Core) {
  cy.elements().removeClass(CLASSES);
}
