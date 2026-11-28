import { describe, expect, it, vi } from 'vitest';

import type { SlicerApi } from './api';
import { SlicerStore } from './store.svelte';
import { everybodysJobs, someSlicers } from './test/fakes';

/** An api whose answers a test sets, each a promise or a thrown error. */
const apiWith = (overrides: Partial<SlicerApi> = {}): SlicerApi => ({
  listSlicers: vi.fn(() => Promise.resolve(someSlicers)),
  listJobs: vi.fn(() => Promise.resolve(everybodysJobs)),
  installSlicer: vi.fn(() => Promise.resolve(someSlicers)),
  refreshSlicers: vi.fn(() => Promise.resolve(someSlicers)),
  removeSlicer: vi.fn(() => Promise.resolve(undefined)),
  pinSlicer: vi.fn(() => Promise.resolve(someSlicers)),
  cancelJob: vi.fn(() => Promise.resolve(everybodysJobs.jobs[0]!)),
  submitJob: vi.fn(() => Promise.resolve(everybodysJobs.jobs[0]!)),
  readJob: vi.fn(() => Promise.resolve(everybodysJobs.jobs[0]!)),
  checkPrintQueue: vi.fn(() => Promise.resolve(undefined)),
  enqueueJob: vi.fn(() => Promise.resolve({})),
  ...overrides,
});

const refusal = (status: number, code: string) =>
  Object.assign(new Error('refused'), { status, code });

describe('the slicer store', () => {
  it('reads the slicers and the jobs', async () => {
    const store = new SlicerStore(apiWith());
    await store.refresh();

    expect(store.load).toBe('ready');
    expect(store.slicers?.default).toBe('2.9.6');
    expect(store.jobs?.all).toBe(true);
  });

  it('tells a refusal from a failure', async () => {
    const refused = new SlicerStore(
      apiWith({
        listJobs: () => Promise.reject(refusal(403, 'core.auth/ACCESS_DENIED')),
      }),
    );
    await refused.refresh();
    expect(refused.load).toBe('refused');

    const failed = new SlicerStore(
      apiWith({
        listSlicers: () =>
          Promise.reject(refusal(503, 'server-slicing/NOT_READY')),
      }),
    );
    await failed.refresh();
    expect(failed.load).toBe('failed');
    expect(failed.fault?.code).toBe('server-slicing/NOT_READY');
  });

  it('keeps an action that failed above the lists, and reads them again', async () => {
    const api = apiWith({
      installSlicer: () =>
        Promise.reject(refusal(403, 'server-slicing/NOT_ADMINISTRATOR')),
    });
    const store = new SlicerStore(api);

    await store.install('3.0.0-alpha12');

    expect(store.actionFault?.code).toBe('server-slicing/NOT_ADMINISTRATOR');
    expect(api.listSlicers).toHaveBeenCalled();

    await store.pin('2.9.6');
    expect(store.actionFault).toBeUndefined();
    expect(api.pinSlicer).toHaveBeenCalledWith(
      { body: { version: '2.9.6' } },
      expect.anything(),
    );
  });

  it('stops reading once disposed', async () => {
    vi.useFakeTimers();
    const api = apiWith();
    const store = new SlicerStore(api);

    store.start();
    store.dispose();
    await vi.advanceTimersByTimeAsync(10_000);

    expect(api.listSlicers).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });
  it('answers the id of a job it queued, and nothing for a refused one', async () => {
    const queued = new SlicerStore(apiWith());
    const body = { source: { storage: 'local', path: 'box.3mf' } };

    await expect(queued.submit(body)).resolves.toBe(everybodysJobs.jobs[0]!.id);
    expect(queued.actionFault).toBeUndefined();

    const refused = new SlicerStore(
      apiWith({
        submitJob: vi.fn(() =>
          Promise.reject(refusal(403, 'core.authz/FORBIDDEN')),
        ),
      }),
    );

    await expect(refused.submit(body)).resolves.toBeUndefined();
    expect(refused.actionFault).toBeDefined();
  });
});
