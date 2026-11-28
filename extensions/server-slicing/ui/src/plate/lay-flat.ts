// Lay flat: the flat face of a mesh under the cursor, and the rotation that
// puts that face on the bed.
//
// A face is every triangle of the mesh in the same plane as the one pointed
// at: the same normal within LAY_FLAT_DEGREES and the same distance from the
// origin within LAY_FLAT_MM. Two coplanar islands, such as the two feet of a
// bridge, are one face, because both rest on the bed together.
import {
  Matrix3,
  Matrix4,
  Quaternion,
  Vector3,
  type BufferGeometry,
} from 'three';

/** How far apart two normals may point and still be one face. */
export const LAY_FLAT_DEGREES = 1;
/** How far apart two parallel planes may be and still be one face. */
export const LAY_FLAT_MM = 0.05;

const DOWN = new Vector3(0, 0, -1);

/** The unit normal and plane offset of every triangle, read once per mesh. */
interface Planes {
  readonly normals: Float32Array;
  readonly offsets: Float32Array;
}

const planesOf = new WeakMap<BufferGeometry, Planes>();

const cornerOf = (geometry: BufferGeometry, at: number): number =>
  geometry.index?.getX(at) ?? at;

const readPlanes = (geometry: BufferGeometry): Planes => {
  const known = planesOf.get(geometry);
  if (known) return known;

  const position = geometry.getAttribute('position');
  const count = (geometry.index?.count ?? position.count) / 3;
  const normals = new Float32Array(count * 3);
  const offsets = new Float32Array(count);
  const a = new Vector3();
  const b = new Vector3();
  const c = new Vector3();
  for (let face = 0; face < count; face += 1) {
    a.fromBufferAttribute(position, cornerOf(geometry, face * 3));
    b.fromBufferAttribute(position, cornerOf(geometry, face * 3 + 1));
    c.fromBufferAttribute(position, cornerOf(geometry, face * 3 + 2));
    c.sub(b);
    a.sub(b);
    c.cross(a).normalize();
    normals.set([c.x, c.y, c.z], face * 3);
    offsets[face] = c.dot(b);
  }
  const planes = { normals, offsets };
  planesOf.set(geometry, planes);
  return planes;
};

/** The unit outward normal of one triangle, in the mesh's own coordinates. */
export const faceNormal = (geometry: BufferGeometry, face: number): Vector3 => {
  const { normals } = readPlanes(geometry);
  return new Vector3(
    normals[face * 3] ?? 0,
    normals[face * 3 + 1] ?? 0,
    normals[face * 3 + 2] ?? -1,
  );
};

/** Every triangle in the plane of `face`, `face` included. */
export const coplanarFaces = (
  geometry: BufferGeometry,
  face: number,
): number[] => {
  const { normals, offsets } = readPlanes(geometry);
  const nx = normals[face * 3] ?? 0;
  const ny = normals[face * 3 + 1] ?? 0;
  const nz = normals[face * 3 + 2] ?? 0;
  const offset = offsets[face] ?? 0;
  const cosine = Math.cos((LAY_FLAT_DEGREES * Math.PI) / 180);
  const faces: number[] = [];
  for (let other = 0; other < offsets.length; other += 1) {
    const alike =
      nx * (normals[other * 3] ?? 0) +
      ny * (normals[other * 3 + 1] ?? 0) +
      nz * (normals[other * 3 + 2] ?? 0);
    if (
      alike >= cosine &&
      Math.abs((offsets[other] ?? 0) - offset) <= LAY_FLAT_MM
    )
      faces.push(other);
  }
  return faces;
};

/**
 * The rotation that turns a part so the face with `normal`, in the mesh's own
 * coordinates, points straight down. It is applied after the part's present
 * rotation, so a scaled part stays scaled.
 */
export const layFlatRotation = (
  rotation: Matrix4,
  normal: Vector3,
): Matrix4 => {
  const turned = normal
    .clone()
    .applyMatrix3(new Matrix3().getNormalMatrix(rotation))
    .normalize();
  const turn = new Quaternion().setFromUnitVectors(turned, DOWN);
  return new Matrix4().makeRotationFromQuaternion(turn).multiply(rotation);
};
