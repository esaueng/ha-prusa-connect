// What the workspace's stories and tests draw instead of the server: a saved
// profile, a model in Storage, printers in the shape the fleet lists them, and
// an API that answers at once.
import type { Device } from '@prusa/connect-ui/domain';
import { BoxGeometry, Matrix4 } from 'three';

import type { Job, ProfileSettings, STLBed } from '../../api';
import { makeSTL } from './model';
import type { PrepareApi } from './prepare-api';

export const bed = {
  min_x: 0,
  min_y: 0,
  max_x: 250,
  max_y: 220,
  height: 270,
} satisfies STLBed;

export const profileSettings: Record<string, ProfileSettings> = {
  'core-one-petg': {
    filament_type: 'PETG',
    layer_height_mm: 0.2,
    infill_percent: 15,
    walls: 2,
    supports: 'off',
    brim_width_mm: 0,
  },
};

/** A box 10 × 20 × 30 mm, saved as a plain STL. */
export const modelFile = (name = 'bracket.stl'): File => {
  const geometry = new BoxGeometry(10, 20, 30);
  const file = new File(
    [makeSTL([{ geometry, matrix: new Matrix4() }], false)],
    name,
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

export const slicedJob: Job = {
  ...placedJob,
  state: 'done',
  progress: 100,
  result: {
    storage: 'local',
    path: 'sliced/3f9a01c2-bracket-placed-core-one-petg.gcode',
    size: 1024,
    format: 'gcode',
    slicer: '2.9.6',
    duration_ms: 2400,
    estimated_seconds: 12_300,
    objects: [],
    thumbnails: [],
  },
};

export const testPrepare: PrepareApi = {
  destinations: () =>
    Promise.resolve([{ name: 'local', label: 'Local files' }]),
  read: () => Promise.resolve(modelFile()),
  save: (_storage, name) => Promise.resolve(name),
  submit: () => Promise.resolve(placedJob),
  job: () => Promise.resolve(slicedJob),
  download: () => Promise.resolve(new Blob(['G28\n'])),
  print: () => Promise.resolve(),
  checkQueue: () => Promise.resolve(undefined),
  enqueue: () => Promise.resolve({ waiting: 2, position: 2 }),
};

const START_PRINT = 'core.command.start_print';

/** One printer as the fleet lists it, in the state `state` with PETG loaded. */
export const printer = (
  id: string,
  name: string,
  state: string,
  more: Partial<Device> = {},
): Device => ({
  actions: [],
  authorization_epoch: 1,
  capabilities: [START_PRINT],
  capability_epoch: 1,
  connected: true,
  driver: 'xbuddy',
  firmware: '6.4.0',
  id,
  issues: [],
  kind: 'printer',
  labels: {},
  tags: [],
  verbs: [START_PRINT],
  link: {
    counted: false,
    dropped_telemetry: 0,
    dropped_telemetry_per_minute: 0,
    measured_over_seconds: 60,
    refused_frames: 0,
    refused_frames_per_minute: 0,
  },
  model: 'CORE One',
  name,
  profile_matched: true,
  serial: `serial-${id}`,
  state: {
    '/state': state,
    '/tools/0/material_package_instance/package/material/abbreviation': 'PETG',
  },
  unit_uid: `unit-${id}`,
  vendor: 'Prusa',
  build_volume: { x_mm: 250, y_mm: 220, z_mm: 270 },
  ...more,
});

export const henry = printer('henry', 'Henry', 'IDLE');
export const charlie = printer('charlie', 'Charlie', 'PRINTING', {
  model: 'MK4S',
  build_volume: { x_mm: 250, y_mm: 210, z_mm: 220 },
  job: {
    device_job_id: '12',
    display_name: 'clip.bgcode',
    id: 'job-12',
    path: 'usb/clip.bgcode',
    progress: 63,
    running: true,
    state: 'PRINTING',
    time_remaining_ms: 3_600_000,
  },
});
export const xl = printer('xl', 'Xavier', 'IDLE', {
  model: 'XL',
  build_volume: { x_mm: 360, y_mm: 360, z_mm: 360 },
});

/** A remembered store that keeps nothing between tests. */
export const memory = (): {
  read(key: string): string | null;
  write(key: string, value: string): void;
} => {
  const held = new Map<string, string>();
  return {
    read: (key) => held.get(key) ?? null,
    write: (key, value) => {
      held.set(key, value);
    },
  };
};
