import { BoxGeometry, Matrix4 } from 'three';
import { STLLoader } from 'three/addons/loaders/STLLoader.js';
import { describe, expect, it, vi } from 'vitest';

import type { Job } from '../api';
import {
  placedJob,
  slicedJob,
  testPrepare,
} from '../views/prepare-panel/prepare-fixtures';
import type { PrepareApi } from '../views/prepare-panel/prepare-api';
import {
  meshKey,
  PlateFlow,
  type PlateInput,
  signatureOf,
} from './plate-flow.svelte';

const geometry = new BoxGeometry(10, 20, 30);

const inputOf = (x = 50, settings?: PlateInput['settings']): PlateInput => {
  const meshes = [
    {
      id: 'part-1',
      geometry,
      matrix: new Matrix4().makeTranslation(x, 50, 15),
    },
  ];
  return {
    meshKey: meshKey(meshes),
    name: 'bracket.stl',
    meshes,
    storage: 'local',
    profile: 'core-one-petg',
    settings,
  };
};

const running: Job = { ...placedJob, state: 'running', progress: 40 };

/** An API that answers like the fixtures and records each call. */
const recording = (more: Partial<PrepareApi> = {}) => ({
  destinations: vi.fn(more.destinations ?? testPrepare.destinations),
  read: vi.fn(more.read ?? testPrepare.read),
  save: vi.fn(more.save ?? testPrepare.save),
  submit: vi.fn(more.submit ?? testPrepare.submit),
  job: vi.fn(more.job ?? testPrepare.job),
  download: vi.fn(more.download ?? testPrepare.download),
  print: vi.fn(more.print ?? testPrepare.print),
});

describe('slicing and printing the plate', () => {
  it('saves the plate, waits for the job and prints its G-code', async () => {
    const answers = [running, slicedJob];
    const api = recording({
      job: vi.fn(() => Promise.resolve(answers.shift() ?? slicedJob)),
    });
    const flow = new PlateFlow(api, new AbortController().signal, 1);
    const input = inputOf(50, { infill_percent: 30 });

    await expect(
      flow.print(input, { id: 'henry', name: 'Henry' }),
    ).resolves.toBe(true);

    const [storage, name, body] = api.save.mock.calls[0] ?? [];
    expect(storage).toBe('local');
    expect(name).toMatch(/^bracket-placed-[0-9a-f]{32}\.stl$/u);
    expect(api.submit).toHaveBeenCalledWith(
      {
        storage: 'local',
        path: name,
        profile: 'core-one-petg',
        settings: { infill_percent: 30 },
      },
      expect.any(AbortSignal),
    );
    expect(api.job).toHaveBeenCalledTimes(2);
    expect(api.print).toHaveBeenCalledWith({
      deviceId: 'henry',
      storage: 'local',
      source: slicedJob.result?.path,
      name: '3f9a01c2-bracket-placed-core-one-petg.gcode',
    });
    expect(flow.notice).toEqual({
      key: 'slicer.plate.notice.printing',
      params: { printer: 'Henry' },
    });
    expect(flow.step).toBe('idle');
    expect(flow.slicedFor(signatureOf(input))).toEqual(slicedJob);

    // The saved STL holds the box where the plate put it.
    if (!(body instanceof Blob)) throw new Error('no STL was saved');
    const written = new STLLoader().parse(await body.arrayBuffer());
    written.computeBoundingBox();
    expect(written.boundingBox?.min.toArray()).toEqual([45, 40, 0]);
    written.dispose();
  });

  it('reuses the G-code of a plate that has not changed', async () => {
    const api = recording();
    const flow = new PlateFlow(api, new AbortController().signal, 1);

    await flow.slice(inputOf());
    await flow.download(inputOf());
    await flow.print(inputOf(), { id: 'henry', name: 'Henry' });
    expect(api.save).toHaveBeenCalledTimes(1);
    expect(api.submit).toHaveBeenCalledTimes(1);

    // New settings slice again from the STL already saved.
    await flow.slice(inputOf(50, { walls: 4 }));
    expect(api.save).toHaveBeenCalledTimes(1);
    expect(api.submit).toHaveBeenCalledTimes(2);

    // A part that moved is a new STL.
    await flow.slice(inputOf(60));
    expect(api.save).toHaveBeenCalledTimes(2);
    expect(flow.slicedFor(signatureOf(inputOf()))).toBeUndefined();
  });

  it('keeps the fault of a job that failed and prints nothing', async () => {
    const failed: Job = {
      ...placedJob,
      state: 'failed',
      error: {
        code: 'server-slicing/SLICER_FAILED',
        message: 'PrusaSlicer stopped',
      },
    };
    const api = recording({ job: vi.fn(() => Promise.resolve(failed)) });
    const flow = new PlateFlow(api, new AbortController().signal, 1);

    await expect(
      flow.print(inputOf(), { id: 'henry', name: 'Henry' }),
    ).resolves.toBe(false);
    expect(api.print).not.toHaveBeenCalled();
    expect(flow.fault?.code).toBe('server-slicing/SLICER_FAILED');
    expect(flow.step).toBe('idle');
  });

  it('stops waiting for a job canceled on its own page', async () => {
    const canceled: Job = { ...placedJob, state: 'canceled' };
    const api = recording({ job: () => Promise.resolve(canceled) });
    const flow = new PlateFlow(api, new AbortController().signal, 1);

    await expect(flow.slice(inputOf())).resolves.toBeUndefined();
    expect(api.job).toHaveBeenCalledTimes(1);
    expect(flow.fault).toBeDefined();
    expect(flow.step).toBe('idle');
  });

  it('keeps the printer’s refusal as the fault', async () => {
    const api = recording({
      print: vi.fn(() =>
        Promise.reject(
          Object.assign(new Error('busy'), { code: 'core.printer/BUSY' }),
        ),
      ),
    });
    const flow = new PlateFlow(api, new AbortController().signal, 1);
    await flow.print(inputOf(), { id: 'henry', name: 'Henry' });
    expect(flow.fault?.code).toBe('core.printer/BUSY');
    expect(flow.notice).toBeUndefined();
  });

  it('runs one step at a time', async () => {
    let finish: (job: Job) => void = () => undefined;
    const api = recording({
      submit: vi.fn(
        () =>
          new Promise<Job>((resolve) => {
            finish = resolve;
          }),
      ),
    });
    const flow = new PlateFlow(api, new AbortController().signal, 1);
    const first = flow.slice(inputOf());
    await vi.waitFor(() => {
      expect(flow.step).toBe('slicing');
    });
    await expect(flow.slice(inputOf(70))).resolves.toBeUndefined();
    finish(slicedJob);
    await first;
    expect(api.submit).toHaveBeenCalledTimes(1);
  });

  it('stops waiting for a job once the workspace closes', async () => {
    const controller = new AbortController();
    const api = recording({
      job: () => Promise.resolve(running),
    });
    const flow = new PlateFlow(api, controller.signal, 5);
    const sliced = flow.slice(inputOf());
    await vi.waitFor(() => {
      expect(api.job).toHaveBeenCalled();
    });
    controller.abort();
    await expect(sliced).resolves.toBeUndefined();
    expect(flow.fault).toBeUndefined();
  });
});
