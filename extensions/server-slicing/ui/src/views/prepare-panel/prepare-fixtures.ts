import { BoxGeometry, Matrix4 } from 'three';

import type { Job, STLBed } from '../../api';
import { makeSTL } from './model';
import type { PrepareApi } from './prepare-api';

export const bed = {
  min_x: 0,
  min_y: 0,
  max_x: 250,
  max_y: 220,
  height: 270,
} satisfies STLBed;

export const modelFile = (): File => {
  const geometry = new BoxGeometry(10, 20, 30);
  const file = new File(
    [makeSTL(geometry, new Matrix4(), false)],
    'bracket.stl',
  );
  geometry.dispose();
  return file;
};

export const loadFixture = async (
  name: string,
  fileName = name,
): Promise<File> => {
  const url = new URL(`./testdata/${name}`, import.meta.url);
  // eslint-disable-next-line no-restricted-globals -- The fixture is a bundled asset and calls no core API.
  const response = await fetch(url);
  if (!response.ok) throw new Error(`Could not load fixture ${name}`);
  return new File([await response.blob()], fileName);
};

export const assembledFile = (): Promise<File> => loadFixture('assembled.3mf');

export const placedJob: Job = {
  id: 'placed-job',
  state: 'queued',
  progress: 0,
  source: { storage: 'local', path: 'bracket-placed.stl' },
  output: { storage: 'local' },
  by: 'slicer',
  selection: {},
  overwrite: false,
  created_at: '2026-11-24T12:00:00Z',
};

export const testPrepare: PrepareApi = {
  destinations: () =>
    Promise.resolve([{ name: 'local', label: 'Local files' }]),
  read: () => Promise.resolve(modelFile()),
  save: (_storage, name) => Promise.resolve(name),
  submit: () => Promise.resolve(placedJob),
};
