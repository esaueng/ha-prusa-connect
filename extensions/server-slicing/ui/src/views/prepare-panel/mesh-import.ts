import { BufferAttribute, BufferGeometry, Vector3, type Matrix4 } from 'three';

import { threeMFReader } from './import-3mf';
import {
  MAX_COORDINATE_MM,
  MAX_MODEL_BYTES,
  MAX_TRIANGLES,
  ModelLimitError,
} from './import-limits';
import { stlReader } from './import-stl';

/** A triangle mesh in millimetres, optionally placed by an affine matrix. */
export interface MeshPart {
  geometry: BufferGeometry;
  transform?: Matrix4;
}

export interface ImportedMesh {
  parts: readonly MeshPart[];
  /** Use the file's position as the initial position on the plate. */
  preservePlacement: boolean;
}

/** Register by extension. Check the byte signature before decoding the file. */
export interface MeshReader {
  matches(bytes: Uint8Array): boolean;
  /** Transfers ownership of the returned geometries to Prepare. */
  read(bytes: Uint8Array): ImportedMesh | Promise<ImportedMesh>;
}

const readers = new Map<string, MeshReader>([
  ['stl', stlReader],
  ['3mf', threeMFReader],
]);

export const readMeshFile = async (file: File): Promise<ImportedMesh> => {
  if (file.size > MAX_MODEL_BYTES)
    throw new ModelLimitError('Model exceeds 20 MiB');
  const extension = /\.([^.]+)$/u.exec(file.name)?.[1]?.toLowerCase();
  const reader = extension && readers.get(extension);
  if (!reader) throw new Error('Unsupported model extension');

  const bytes = new Uint8Array(await file.arrayBuffer());
  if (!reader.matches(bytes)) throw new Error('Model signature does not match');
  return reader.read(bytes);
};

const validCoordinate = (value: number): boolean =>
  Math.abs(value) <= MAX_COORDINATE_MM;

const triangleCorners = ({ geometry }: MeshPart): number =>
  geometry.index?.count ?? geometry.getAttribute('position').count;

/** Prepare edits one mesh. Apply the instance transforms and keep every triangle. */
export const combineParts = (parts: readonly MeshPart[]): BufferGeometry => {
  const corners = parts.reduce(
    (total, part) => total + triangleCorners(part),
    0,
  );
  if (corners / 3 > MAX_TRIANGLES)
    throw new ModelLimitError('Model has more than 300,000 triangles');
  if (!corners || parts.some((part) => triangleCorners(part) % 3 !== 0))
    throw new Error('Invalid triangle count');

  const [single] = parts;
  if (parts.length === 1 && single && !single.transform) {
    const position = single.geometry.getAttribute('position');
    if (!position.array.every(validCoordinate))
      throw new Error('Invalid model coordinate');
    if (
      single.geometry.index &&
      !single.geometry.index.array.every(
        (index) =>
          Number.isInteger(index) && index >= 0 && index < position.count,
      )
    )
      throw new Error('Invalid triangle index');
    if (!single.geometry.hasAttribute('normal'))
      single.geometry.computeVertexNormals();
    return single.geometry;
  }

  const positions = new Float32Array(corners * 3);
  const point = new Vector3();
  let offset = 0;
  for (const { geometry, transform } of parts) {
    const source = geometry.getAttribute('position');
    const count = geometry.index?.count ?? source.count;
    const determinant = transform?.determinant() ?? 1;
    if (!Number.isFinite(determinant) || determinant === 0)
      throw new Error('Invalid model transform');
    for (let triangle = 0; triangle < count; triangle += 3) {
      for (let corner = 0; corner < 3; corner += 1) {
        // A reflected instance keeps the orientation of its faces.
        const at =
          triangle + (determinant < 0 && corner > 0 ? 3 - corner : corner);
        point.fromBufferAttribute(source, geometry.index?.getX(at) ?? at);
        if (transform) point.applyMatrix4(transform);
        if (
          !validCoordinate(
            Math.max(Math.abs(point.x), Math.abs(point.y), Math.abs(point.z)),
          )
        )
          throw new Error('Invalid model coordinate');
        point.toArray(positions, offset);
        offset += 3;
      }
    }
  }

  const geometry = new BufferGeometry().setAttribute(
    'position',
    new BufferAttribute(positions, 3),
  );
  geometry.computeVertexNormals();
  return geometry;
};
