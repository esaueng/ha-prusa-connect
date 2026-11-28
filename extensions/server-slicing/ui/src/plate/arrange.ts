// Free space on the bed, read from the parts' bounding boxes.
//
// A part takes the rectangle of its footprint and a gap around it. It goes to
// the free spot nearest the target, on a grid of ARRANGE_STEP_MM. A part shaped
// like an L takes its whole box, so two of them never nest. That keeps the
// search simple and fast, and the bed holds fewer parts than a slicer that
// arranges by outline.
import type { STLBed } from '../api';
import { bedCentre, type PartSize, type Rect } from './part';

/** The grid the search walks. */
export const ARRANGE_STEP_MM = 2;
/** The space kept between two parts. */
export const ARRANGE_GAP_MM = 6;

const overlaps = (a: Rect, b: Rect, gap: number): boolean =>
  a.minX < b.maxX + gap &&
  b.minX < a.maxX + gap &&
  a.minY < b.maxY + gap &&
  b.minY < a.maxY + gap;

/**
 * The centre of the free spot nearest `target` where a part of `size` stands
 * inside the bed and clear of every obstacle, or undefined when there is none.
 */
export const findFree = (
  size: PartSize,
  obstacles: readonly Rect[],
  bed: STLBed,
  target: readonly [number, number] = bedCentre(bed),
): [number, number] | undefined => {
  const lowX = bed.min_x + size.halfX;
  const highX = bed.max_x - size.halfX;
  const lowY = bed.min_y + size.halfY;
  const highY = bed.max_y - size.halfY;
  if (lowX > highX || lowY > highY || size.height > bed.height)
    return undefined;

  // The grid is laid from the target, so a target that is free is taken as
  // it is, and the spots on the edges of the bed are tried too.
  const xs = steps(target[0], lowX, highX);
  const ys = steps(target[1], lowY, highY);
  const spots: [number, number, number][] = [];
  for (const x of xs) {
    for (const y of ys) {
      spots.push([x, y, (x - target[0]) ** 2 + (y - target[1]) ** 2]);
    }
  }
  spots.sort((a, b) => a[2] - b[2]);

  for (const [x, y] of spots) {
    const rect = rectAt([x, y], size);
    if (!obstacles.some((other) => overlaps(rect, other, ARRANGE_GAP_MM)))
      return [x, y];
  }
  return undefined;
};

const steps = (from: number, low: number, high: number): number[] => {
  const start = Math.min(Math.max(from, low), high);
  const out = new Set<number>([low, high, start]);
  for (let at = start - ARRANGE_STEP_MM; at > low; at -= ARRANGE_STEP_MM)
    out.add(at);
  for (let at = start + ARRANGE_STEP_MM; at < high; at += ARRANGE_STEP_MM)
    out.add(at);
  return [...out];
};

export interface ArrangeItem {
  readonly id: string;
  readonly size: PartSize;
}

/**
 * Places each item clear of the fixed rectangles and of each other, the
 * largest first. With nothing fixed, the items are packed from the front left
 * corner and the group is then moved to the middle of the bed, which leaves
 * more room than packing around the middle. With fixed parts, each item takes
 * the free spot nearest the middle. An item with no free spot is named under
 * `unplaced` and keeps where it was.
 */
export const arrange = (
  items: readonly ArrangeItem[],
  fixed: readonly Rect[],
  bed: STLBed,
): { placed: Map<string, [number, number]>; unplaced: string[] } => {
  const order = [...items].sort(
    (a, b) => b.size.halfX * b.size.halfY - a.size.halfX * a.size.halfY,
  );
  const target: [number, number] =
    fixed.length === 0 ? [bed.min_x, bed.min_y] : bedCentre(bed);
  const taken = [...fixed];
  const placed = new Map<string, [number, number]>();
  const unplaced: string[] = [];
  for (const item of order) {
    const spot = findFree(item.size, taken, bed, target);
    if (!spot) {
      unplaced.push(item.id);
      continue;
    }
    placed.set(item.id, spot);
    taken.push(rectAt(spot, item.size));
  }

  if (fixed.length === 0 && placed.size > 0) {
    const group = taken.reduce((all, one) => ({
      minX: Math.min(all.minX, one.minX),
      minY: Math.min(all.minY, one.minY),
      maxX: Math.max(all.maxX, one.maxX),
      maxY: Math.max(all.maxY, one.maxY),
    }));
    const [cx, cy] = bedCentre(bed);
    const dx = cx - (group.minX + group.maxX) / 2;
    const dy = cy - (group.minY + group.maxY) / 2;
    for (const [id, [x, y]] of placed) placed.set(id, [x + dx, y + dy]);
  }
  return { placed, unplaced };
};

const rectAt = ([x, y]: [number, number], size: PartSize): Rect => ({
  minX: x - size.halfX,
  minY: y - size.halfY,
  maxX: x + size.halfX,
  maxY: y + size.halfY,
});
