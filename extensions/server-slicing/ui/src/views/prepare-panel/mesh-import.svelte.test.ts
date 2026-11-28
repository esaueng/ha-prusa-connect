import { Vector3 } from 'three';
import { expect, it } from 'vitest';

import { MAX_MODEL_BYTES, ModelLimitError } from './import-limits';
import { combineParts, readMeshFile } from './mesh-import';
import { readModel } from './model';

import { loadFixture } from './prepare-fixtures';

it('keeps repeated instances and composes build and nested component transforms in millimetres', async () => {
  const model = await readMeshFile(
    await loadFixture('assembled.3mf', 'PLATE.3MF'),
  );
  expect(model.preservePlacement).toBe(true);
  expect(model.parts).toHaveLength(2);
  const [rotated, mirrored] = model.parts;
  if (!rotated?.transform || !mirrored?.transform)
    throw new Error('Missing instance transforms');

  expect(rotated.geometry).toBe(mirrored.geometry);
  const position = rotated.geometry.getAttribute('position');
  expect(new Vector3().fromBufferAttribute(position, 1).toArray()).toEqual([
    10, 0, 0,
  ]);
  const point = new Vector3(10, 0, 0).applyMatrix4(rotated.transform);
  expect(point.x).toBeCloseTo(76.2);
  expect(point.y).toBeCloseTo(86.2);
  expect(point.z).toBe(0);
  const reflected = new Vector3(10, 0, 0).applyMatrix4(mirrored.transform);
  expect(reflected.x).toBeCloseTo(117);
  expect(reflected.y).toBeCloseTo(25.4);
  expect(reflected.z).toBeCloseTo(12.7);

  const geometry = combineParts(model.parts);
  const result = geometry.getAttribute('position');
  expect(result.count).toBe(24);
  // The bottom face of the mirror still points down.
  const a = new Vector3().fromBufferAttribute(result, 12);
  const b = new Vector3().fromBufferAttribute(result, 13);
  const c = new Vector3().fromBufferAttribute(result, 14);
  expect(b.sub(a).cross(c.sub(a)).z).toBeLessThan(0);
  geometry.dispose();
  rotated.geometry.dispose();
});

it.each([
  ['bad-index.3mf', 'missing triangle vertex'],
  ['cycle.3mf', 'circular object reference'],
  ['malformed-xml.3mf', 'malformed XML'],
  ['doctype.3mf', 'XML document type declaration'],
])('rejects malformed 3MF %s', async (name, message) => {
  await expect(readMeshFile(await loadFixture(name))).rejects.toThrow(message);
});

it.each([
  ['large-archive.3mf', 'exceeds 64 MiB'],
  ['lying-archive.3mf', 'beyond its declared size'],
  ['too-many-triangles.3mf', 'more than 300,000 triangles'],
])('enforces the import limit for %s', async (name, message) => {
  const result = readMeshFile(await loadFixture(name));
  await expect(result).rejects.toBeInstanceOf(ModelLimitError);
  await expect(result).rejects.toThrow(message);
});

it('checks the file size and matches the extension to the file signature', async () => {
  const large = new File([new Uint8Array(MAX_MODEL_BYTES + 1)], 'large.3mf');
  await expect(readMeshFile(large)).rejects.toBeInstanceOf(ModelLimitError);
  await expect(
    readMeshFile(await loadFixture('plain.stl', 'renamed.3mf')),
  ).rejects.toThrow('signature');
  await expect(
    readMeshFile(await loadFixture('assembled.3mf', 'renamed.stl')),
  ).rejects.toThrow('signature');
});

it('centres a plain ASCII STL in the editor and leaves it without saved placement', async () => {
  const { geometry, placedAt, floor } = await readModel(
    await loadFixture('plain.stl', 'PLAIN.STL'),
  );
  expect(placedAt).toBeUndefined();
  expect(floor).toBe(0);
  expect(geometry.boundingBox?.min.toArray()).toEqual([-2, -3, -2]);
  expect(geometry.boundingBox?.max.toArray()).toEqual([2, 3, 2]);
  geometry.dispose();
});
