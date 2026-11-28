// One object on the build plate, and the arithmetic of where it stands.
//
// A part keeps its mesh as it was read and a rotation of it. `local` turns the
// rotated mesh so its box is centred on the origin in X and Y and its lowest
// point is at Z 0, so `x` and `y` are where the centre of its footprint stands
// on the bed. `z` lifts a part off the bed: a part of a 3MF assembly that
// stands on another part keeps its height, as the slicer keeps it within the
// one object the plate is saved as. All lengths are millimetres.
import { Box3, Matrix4, Vector3, type BufferGeometry } from 'three';

import type { STLBed } from '../api';

/** The half sizes of a part's footprint and its height, around its own centre. */
export interface PartSize {
  readonly halfX: number;
  readonly halfY: number;
  readonly height: number;
}

export interface Part {
  readonly id: string;
  readonly name: string;
  readonly geometry: BufferGeometry;
  /** The rotation and scale of the mesh, with no translation. */
  readonly rotation: Matrix4;
  /** `rotation`, then the offset that centres the footprint and drops it to Z 0. */
  readonly local: Matrix4;
  readonly size: PartSize;
  readonly x: number;
  readonly y: number;
  /** How far the part's lowest point is above the bed. */
  readonly z: number;
  /** The part's size against the file's, in percent, the same on every axis. */
  readonly scale: number;
}

/** A rectangle on the bed. */
export interface Rect {
  readonly minX: number;
  readonly minY: number;
  readonly maxX: number;
  readonly maxY: number;
}

/** The rotated mesh's box, read from its vertices so a turned part has a tight one. */
const rotatedBounds = (geometry: BufferGeometry, rotation: Matrix4): Box3 => {
  const points = geometry.getAttribute('position');
  const bounds = new Box3();
  const point = new Vector3();
  for (let i = 0; i < points.count; i += 1) {
    bounds.expandByPoint(
      point.fromBufferAttribute(points, i).applyMatrix4(rotation),
    );
  }
  return bounds;
};

/**
 * Orients a mesh by a rotation. `centre` is where the middle of the rotated
 * box's footprint is in the mesh's own coordinates, which is where a mesh that
 * keeps its placement from the file stands on the bed.
 */
export const orient = (
  geometry: BufferGeometry,
  rotation: Matrix4,
): {
  local: Matrix4;
  size: PartSize;
  centre: [number, number];
  floor: number;
} => {
  const bounds = rotatedBounds(geometry, rotation);
  const centre = bounds.getCenter(new Vector3());
  const local = new Matrix4()
    .makeTranslation(-centre.x, -centre.y, -bounds.min.z)
    .multiply(rotation);
  return {
    local,
    size: {
      halfX: (bounds.max.x - bounds.min.x) / 2,
      halfY: (bounds.max.y - bounds.min.y) / 2,
      height: bounds.max.z - bounds.min.z,
    },
    centre: [centre.x, centre.y],
    floor: bounds.min.z,
  };
};

/**
 * The same part turned by another rotation, its footprint still centred where
 * it was, and dropped onto the bed.
 */
export const rotated = (part: Part, rotation: Matrix4): Part => {
  const { local, size } = orient(part.geometry, rotation);
  return { ...part, rotation, local, size, z: 0 };
};

/** Where the mesh is drawn and exported: the part's `local`, moved to its place. */
export const worldMatrix = (part: Part): Matrix4 =>
  new Matrix4().makeTranslation(part.x, part.y, part.z).multiply(part.local);

export const footprintOf = (part: Pick<Part, 'size' | 'x' | 'y'>): Rect => ({
  minX: part.x - part.size.halfX,
  minY: part.y - part.size.halfY,
  maxX: part.x + part.size.halfX,
  maxY: part.y + part.size.halfY,
});

/** A tenth of a micrometre, so a part placed exactly at the edge counts as inside. */
const EDGE = 1e-4;

/** True when the whole part stands inside the print volume. */
export const insideBed = (part: Part, bed: STLBed): boolean => {
  const rect = footprintOf(part);
  return (
    rect.minX >= bed.min_x - EDGE &&
    rect.minY >= bed.min_y - EDGE &&
    rect.maxX <= bed.max_x + EDGE &&
    rect.maxY <= bed.max_y + EDGE &&
    part.size.height > 0 &&
    part.z + part.size.height <= bed.height + EDGE
  );
};

export const bedCentre = (bed: STLBed): [number, number] => [
  (bed.min_x + bed.max_x) / 2,
  (bed.min_y + bed.max_y) / 2,
];

/** How many triangles a mesh draws. */
export const triangleCount = (geometry: BufferGeometry): number =>
  (geometry.index?.count ?? geometry.getAttribute('position').count) / 3;
