import {
  Box3,
  Euler,
  Matrix4,
  Mesh,
  MeshBasicMaterial,
  Vector3,
  type BufferGeometry,
} from 'three';
import { STLExporter } from 'three/addons/exporters/STLExporter.js';

import type { STLBed } from '../../api';
import { PLACED_HEADER } from './import-limits';
import { combineParts, readMeshFile } from './mesh-import';

const RADIANS = Math.PI / 180;

export interface Orientation {
  rx: number;
  ry: number;
  rz: number;
  scale: number;
}

export const upright: Orientation = { rx: 0, ry: 0, rz: 0, scale: 100 };

export const readModel = async (
  file: File,
): Promise<{
  geometry: BufferGeometry;
  placedAt: [number, number] | undefined;
  floor: number;
}> => {
  const { parts, preservePlacement } = await readMeshFile(file);
  const imported = new Set(parts.map(({ geometry }) => geometry));
  let geometry: BufferGeometry | undefined;
  try {
    geometry = combineParts(parts);
    let placedAt: [number, number] | undefined;
    let floor = 0;
    if (preservePlacement) {
      geometry.computeBoundingBox();
      const bounds = geometry.boundingBox;
      if (bounds) {
        const center = bounds.getCenter(new Vector3());
        placedAt = [center.x, center.y];
        floor = bounds.min.z;
      }
    }
    geometry.center();
    return { geometry, placedAt, floor };
  } finally {
    for (const part of imported) if (part !== geometry) part.dispose();
  }
};

export const bedCenter = (bed: STLBed): [number, number] => [
  (bed.min_x + bed.max_x) / 2,
  (bed.min_y + bed.max_y) / 2,
];

/** Places the rotated model at the requested Z and centers its XY bounds. */
export const orientedModel = (
  geometry: BufferGeometry,
  { rx, ry, rz, scale }: Orientation,
  floor = 0,
): { matrix: Matrix4; bounds: Box3 } => {
  const matrix = new Matrix4()
    .makeRotationFromEuler(new Euler(rx * RADIANS, ry * RADIANS, rz * RADIANS))
    .scale(new Vector3().setScalar(scale / 100));
  const points = geometry.getAttribute('position');
  const bounds = new Box3();
  const point = new Vector3();
  for (let i = 0; i < points.count; i += 1) {
    point.fromBufferAttribute(points, i).applyMatrix4(matrix);
    bounds.expandByPoint(point);
  }

  const center = bounds.getCenter(point);
  const offset = point.set(-center.x, -center.y, floor - bounds.min.z);
  matrix.setPosition(offset);
  bounds.translate(offset);
  return { matrix, bounds };
};

export const fits = (
  bounds: Box3,
  [x, y]: [number, number],
  bed: STLBed,
): boolean =>
  x + bounds.min.x >= bed.min_x &&
  x + bounds.max.x <= bed.max_x &&
  y + bounds.min.y >= bed.min_y &&
  y + bounds.max.y <= bed.max_y &&
  bounds.min.z >= 0 &&
  bounds.max.z <= bed.height &&
  bounds.max.z > 0;

export const placedMatrix = (
  matrix: Matrix4,
  [x, y]: [number, number],
): Matrix4 => new Matrix4().makeTranslation(x, y, 0).multiply(matrix);

export const makeSTL = (
  model: BufferGeometry,
  matrix: Matrix4,
  placed = true,
): Blob => {
  const material = new MeshBasicMaterial();
  const mesh = new Mesh(model, material);
  mesh.matrixAutoUpdate = false;
  mesh.matrix.copy(matrix);
  mesh.updateMatrixWorld(true);
  const data = new STLExporter().parse(mesh, { binary: true });
  material.dispose();
  const bytes = new Uint8Array(
    data.buffer,
    data.byteOffset,
    data.byteLength,
  ).slice();
  if (placed) bytes.set(PLACED_HEADER);
  return new Blob([bytes], { type: 'application/octet-stream' });
};
