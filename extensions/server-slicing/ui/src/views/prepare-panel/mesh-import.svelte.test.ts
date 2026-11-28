import { Vector3 } from 'three';
import { expect, it } from 'vitest';

import { MAX_MODEL_BYTES, ModelLimitError } from './import-limits';
import { combineParts, readMeshFile } from './mesh-import';
import { Plate } from '../../plate/plate.svelte';

import { bed, loadFixture } from './prepare-fixtures';

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

it('places a plain ASCII STL in free space, without saved placement', async () => {
  const model = await readMeshFile(await loadFixture('plain.stl', 'PLAIN.STL'));
  expect(model.preservePlacement).toBe(false);
  const plate = new Plate(() => bed);
  plate.add(model, 'PLAIN.STL');
  const [part] = plate.parts;
  if (!part) throw new Error('The STL is not on the plate');
  expect([part.x, part.y, part.z]).toEqual([125, 110, 0]);
  expect(part.size).toEqual({ halfX: 2, halfY: 3, height: 4 });
  plate.dispose();
});

it.each(['prusa-volumes.3mf', 'bambu-parts.3mf'])(
  'imports only the solid geometry from %s',
  async (name) => {
    const { parts } = await readMeshFile(await loadFixture(name));
    expect(parts).toHaveLength(1);
    const [part] = parts;
    if (!part) throw new Error('The project has no solid part');
    part.geometry.computeBoundingBox();
    expect(part.geometry.boundingBox?.max.toArray()).toEqual([4, 4, 4]);
    const placed = combineParts(parts);
    expect(placed.getAttribute('position').count).toBe(12);
    placed.computeBoundingBox();
    expect(placed.boundingBox?.min.toArray()).toEqual([20, 30, 0]);
    expect(placed.boundingBox?.max.toArray()).toEqual([24, 34, 4]);
    placed.dispose();
    part.geometry.dispose();
  },
);

it.each(['prusa-incomplete-roles.3mf', 'bambu-unknown-role.3mf'])(
  'rejects %s when its role metadata cannot classify the entire model',
  async (name) => {
    await expect(readMeshFile(await loadFixture(name))).rejects.toThrow(
      'Could not determine the 3MF part roles',
    );
  },
);

it('lifts a 3MF from below the plate and keeps its stored XY position', async () => {
  const plate = new Plate(() => bed);
  plate.add(await readMeshFile(await loadFixture('below-bed.3mf')), 'below');
  const [part] = plate.parts;
  if (!part) throw new Error('The 3MF is not on the plate');
  expect([part.x, part.y, part.z]).toEqual([22, 32, 0]);
  expect(plate.outside).toEqual([]);
  plate.dispose();
});
