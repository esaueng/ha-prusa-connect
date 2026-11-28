import {
  Group,
  Mesh,
  MeshBasicMaterial,
  type BufferGeometry,
  type Matrix4,
} from 'three';
import { STLExporter } from 'three/addons/exporters/STLExporter.js';

import { PLACED_HEADER } from './import-limits';

/** One placed mesh of the STL a plate is saved as. */
export interface PlacedMesh {
  readonly geometry: BufferGeometry;
  readonly matrix: Matrix4;
}

/**
 * Writes meshes as one binary STL, each where its matrix places it. With
 * `placed` the header marks the file as written here, so opening it again
 * keeps its position on the plate.
 */
export const makeSTL = (meshes: readonly PlacedMesh[], placed = true): Blob => {
  const material = new MeshBasicMaterial();
  const group = new Group();
  for (const { geometry, matrix } of meshes) {
    const mesh = new Mesh(geometry, material);
    mesh.matrixAutoUpdate = false;
    mesh.matrix.copy(matrix);
    group.add(mesh);
  }
  group.updateMatrixWorld(true);
  const data = new STLExporter().parse(group, { binary: true });
  material.dispose();
  const bytes = new Uint8Array(
    data.buffer,
    data.byteOffset,
    data.byteLength,
  ).slice();
  if (placed) bytes.set(PLACED_HEADER);
  return new Blob([bytes], { type: 'application/octet-stream' });
};
