// Squarified treemap layout (Bruls, Huizing & van Wijk, 2000).
//
// Pure geometry, no React and no DOM: the layout is the part worth reasoning
// about on its own, and keeping it here means the component is only drawing.
//
// "Squarified" is the whole point: a naive slice-and-dice treemap produces
// slivers whose areas cannot be compared by eye, which defeats the one job a
// treemap has. This packs each row to keep tiles as close to square as it can.

export interface TreemapItem {
  key: string;
  value: number;
}

export interface TreemapTile extends TreemapItem {
  x: number;
  y: number;
  w: number;
  h: number;
}

// worst returns the least-square aspect ratio in a candidate row — the quantity
// the algorithm minimises. `sum` is the row's total area, `short` the length of
// the side it is laid along.
function worst(areas: number[], short: number, sum: number): number {
  if (sum <= 0) return Infinity;
  let max = -Infinity;
  let min = Infinity;
  for (const a of areas) {
    if (a > max) max = a;
    if (a < min) min = a;
  }
  if (min <= 0) return Infinity;
  const s2 = sum * sum;
  const short2 = short * short;
  return Math.max((short2 * max) / s2, s2 / (short2 * min));
}

/**
 * Lays items out as tiles filling `width` × `height`, largest first.
 *
 * Items are taken in the order given — the caller has already ranked them, and
 * re-sorting here would silently disagree with the table beside the chart.
 * Non-positive values are dropped: they have no area to draw, and a zero would
 * make the aspect-ratio arithmetic divide by zero.
 */
export function squarify(
  items: TreemapItem[],
  width: number,
  height: number,
): TreemapTile[] {
  const positive = items.filter((i) => i.value > 0);
  const total = positive.reduce((sum, i) => sum + i.value, 0);
  if (!positive.length || total <= 0 || width <= 0 || height <= 0) return [];

  const scale = (width * height) / total;
  const scaled = positive.map((i) => ({ ...i, area: i.value * scale }));

  const tiles: TreemapTile[] = [];
  // The remaining free rectangle, shrinking one row at a time.
  let x = 0;
  let y = 0;
  let w = width;
  let h = height;
  let i = 0;

  while (i < scaled.length) {
    const short = Math.min(w, h);
    const row = [scaled[i]];
    let rowArea = scaled[i].area;
    let j = i + 1;
    // Grow the row while doing so makes its worst tile MORE square.
    while (j < scaled.length) {
      const candidate = rowArea + scaled[j].area;
      const areas = row.map((r) => r.area);
      if (worst([...areas, scaled[j].area], short, candidate) > worst(areas, short, rowArea)) break;
      row.push(scaled[j]);
      rowArea = candidate;
      j++;
    }

    // Lay the row along the short side, then cut it off the free rectangle.
    const thickness = rowArea / short;
    let offset = 0;
    for (const item of row) {
      const len = item.area / thickness;
      tiles.push(
        w >= h
          ? { key: item.key, value: item.value, x, y: y + offset, w: thickness, h: len }
          : { key: item.key, value: item.value, x: x + offset, y, w: len, h: thickness },
      );
      offset += len;
    }
    if (w >= h) {
      x += thickness;
      w -= thickness;
    } else {
      y += thickness;
      h -= thickness;
    }
    i = j;
  }
  return tiles;
}
