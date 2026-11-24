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
import { STLLoader } from 'three/addons/loaders/STLLoader.js';

import type { STLBed } from '../../api';
import { MAX_MODEL_BYTES } from './prepare-api';

const MAX_TRIANGLES = 300_000;
const MAX_COORDINATE_MM = 1_000_000;
const RADIANS = Math.PI / 180;
const PLACED_HEADER = new TextEncoder().encode('Prusa Connect Prepare');

export interface Orientation {
  rx: number;
  ry: number;
  rz: number;
  scale: number;
}

export const upright: Orientation = { rx: 0, ry: 0, rz: 0, scale: 100 };

export const readModel = async (
  file: Blob,
): Promise<{
  geometry: BufferGeometry;
  placedAt: [number, number] | undefined;
}> => {
  if (file.size < 15 || file.size > MAX_MODEL_BYTES)
    throw new Error('STL size');
  const data = await file.arrayBuffer();
  const geometry = new STLLoader().parse(data);
  const vertices = geometry.getAttribute('position');
  const valid =
    vertices.count >= 3 &&
    vertices.count % 3 === 0 &&
    vertices.count / 3 <= MAX_TRIANGLES &&
    vertices.array.every((n) => Math.abs(n) <= MAX_COORDINATE_MM);
  if (!valid) {
    geometry.dispose();
    throw new Error('STL vertices');
  }

  const bytes = new Uint8Array(data);
  let placedAt: [number, number] | undefined;
  if (PLACED_HEADER.every((value, index) => bytes[index] === value)) {
    geometry.computeBoundingBox();
    const center = geometry.boundingBox?.getCenter(new Vector3());
    if (center) placedAt = [center.x, center.y];
  }
  geometry.center();
  return { geometry, placedAt };
};

export const bedCenter = (bed: STLBed): [number, number] => [
  (bed.min_x + bed.max_x) / 2,
  (bed.min_y + bed.max_y) / 2,
];

/** Uses the rotated vertices to put the model on Z=0 and center its XY bounds. */
export const orientedModel = (
  geometry: BufferGeometry,
  { rx, ry, rz, scale }: Orientation,
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
  matrix.setPosition(-center.x, -center.y, -bounds.min.z);
  bounds.translate(point.set(-center.x, -center.y, -bounds.min.z));
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
