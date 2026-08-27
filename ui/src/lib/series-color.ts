// Colour assignment for the breakdown's part-of-whole charts.
//
// Three schemes, picked by what the dimension MEANS - not one palette applied
// to everything:
//
//   - service   -> the incumbent `serviceColor` hash. Every other trace view
//                  (waterfall, flamegraph, trace list, span detail) already
//                  colours a service that way, and a service that is teal in
//                  the waterfall and blue in the treemap on the same screen is
//                  a worse outcome than any palette gain.
//   - status    -> the reserved status colours. "error" has one colour in this
//                  product and a categorical hue for it would be a lie.
//   - anything  -> the validated eight-slot categorical palette declared in
//     else        globals.css, assigned by ENTITY rather than by rank.
//
// Colour follows the entity, never its rank: assigning slot 1 to whatever is
// currently biggest would repaint every chart the moment a filter changed the
// ranking, and a reader who learned "cart is the blue one" would be wrong on
// the next render.

import { serviceColor } from "@/lib/trace";

// The palette is eight slots in the fixed order declared in globals.css. A
// ninth distinct key does NOT get a generated hue - it shares one, and the
// synthetic tail bucket takes the reserved neutral instead.
export const SERIES_SLOTS = 8;

/** The reserved neutral for the "everything else" bucket. */
export const OTHER_COLOR = "var(--series-other)";

// The product's three-state answer to "did it work?", in the colours the rest
// of the UI already uses for it.
const STATUS_COLORS: Record<string, string> = {
  ok: "var(--color-success)",
  refused: "var(--color-warning)",
  error: "var(--color-error)",
};

// FNV-1a, 32-bit. Small, dependency-free, and - the property that matters here
// - stable across renders, reloads and browsers, so a value keeps its colour.
function hash(key: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < key.length; i++) {
    h ^= key.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

// Assigns palette slots keyed on the value itself. Two values that hash alike
// probe forward to the next free slot, so a chart of eight or fewer never shows
// a duplicate colour; past eight, slots are shared rather than invented.
function categorical(keys: string[]): Map<string, string> {
  const taken = new Array<string | null>(SERIES_SLOTS).fill(null);
  const out = new Map<string, string>();
  for (const key of keys) {
    const start = hash(key) % SERIES_SLOTS;
    let slot = start;
    for (let probe = 0; probe < SERIES_SLOTS; probe++) {
      const candidate = (start + probe) % SERIES_SLOTS;
      if (taken[candidate] === null) {
        slot = candidate;
        break;
      }
    }
    if (taken[slot] === null) taken[slot] = key;
    out.set(key, `var(--series-${slot + 1})`);
  }
  return out;
}

/**
 * Colours for one breakdown's groups, in display order.
 *
 * The empty key - spans carrying no value for this dimension - takes the
 * reserved neutral, the same as the tail: "unlabelled" is not an identity
 * either, and giving it a hue would make it compete with the real values.
 */
export function groupColors(groupBy: string, keys: string[]): Map<string, string> {
  if (groupBy === "service") {
    return new Map(keys.map((k) => [k, k ? serviceColor(k) : OTHER_COLOR]));
  }
  if (groupBy === "status") {
    return new Map(keys.map((k) => [k, STATUS_COLORS[k] ?? OTHER_COLOR]));
  }
  const named = keys.filter((k) => k !== "");
  const colors = categorical(named);
  return new Map(keys.map((k) => [k, k ? (colors.get(k) ?? OTHER_COLOR) : OTHER_COLOR]));
}
