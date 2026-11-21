import type { ModuleHost } from '@prusa/connect-ui/slots';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { addTo, bodyOf, placeOf } from './adding';
import type { Queue, QueueApi } from './api';
import { BoardStore, WAKE_MS } from './board-store.svelte';
import { FilePicker } from './file-picker.svelte';
import { grantsOn } from './grants';
import { OverviewStore } from './overview-store.svelte';
import { REFRESH_MS } from './polling';
import { PrinterQueueStore, type QueueEvents } from './printer-store.svelte';
import {
  emptyQueue,
  fakeApi,
  fakeFilesApi,
  printingHistory,
  printingQueue,
  sendingQueue,
  someBoard,
  someQueues,
  waitingQueue,
} from './test/fakes';

const refusal = (status: number, code: string) =>
  Object.assign(new Error('refused'), { status, code });

const apiWith = (overrides: Partial<QueueApi> = {}): QueueApi => ({
  ...fakeApi(waitingQueue),
  ...overrides,
});

const allowing = {
  allow: vi.fn(() => Promise.resolve()),
  disallow: vi.fn(() => Promise.resolve()),
  may: vi.fn(() => Promise.resolve(true)),
};

afterEach(() => {
  vi.useRealTimers();
});

describe('who may switch printing on ready', () => {
  it('greys the switch for a person who does not administer the printer', async () => {
    const may = vi.fn(() => Promise.resolve(false));
    const store = new PrinterQueueStore(
      apiWith({}),
      { ...allowing, may },
      'mk4-1',
    );
    expect(store.mayAllow).toBe(true);

    store.start();
    await vi.waitFor(() => {
      expect(store.mayAllow).toBe(false);
    });
    store.dispose();
    expect(may).toHaveBeenCalledWith('mk4-1');
  });
});

describe('the store of one printer', () => {
  it('reads the queue of its printer, and its ended prints on their own route', async () => {
    const readQueue = vi.fn(() => Promise.resolve(waitingQueue));
    const readHistory = vi.fn(() =>
      Promise.resolve({ history: [...printingHistory] }),
    );
    const store = new PrinterQueueStore(
      apiWith({ readQueue, readHistory }),
      allowing,
      'mk4-1',
    );
    await store.refresh();
    expect(store.history).toEqual(printingHistory);
    expect(readHistory).toHaveBeenCalledWith(
      { path: { device: 'mk4-1' } },
      expect.any(AbortSignal),
    );

    expect(store.load).toBe('ready');
    expect(store.queue?.entries).toHaveLength(3);
    expect(readQueue).toHaveBeenCalledWith(
      { path: { device: 'mk4-1' } },
      expect.any(AbortSignal),
    );
  });

  it('reads the queue again when the server says its printer changed', async () => {
    const readQueue = vi.fn(() => Promise.resolve(waitingQueue));
    const store = new PrinterQueueStore(
      apiWith({ readQueue }),
      allowing,
      'mk4-1',
    );
    const frame = (device: string) => ({
      type: 'com.prusa.connect.device.transfer.settled',
      envelope: { subject: `device:${device}` },
      payload: { device_id: device },
    });
    // A file landed on another printer, then on this one, and then the
    // stream lost its place, which carries no envelope.
    const frames = [
      frame('xl-2'),
      frame('mk4-1'),
      { type: 'com.prusa.connect.stream.continuity.lost', envelope: undefined },
    ];
    let next = 0;
    const events = vi.fn<(options: { types: readonly string[] }) => unknown>(
      () => ({
        lastEventId: undefined,
        close: () => undefined,
        [Symbol.asyncIterator]: () => ({
          next: () =>
            Promise.resolve(
              next < frames.length
                ? { value: frames[next++], done: false }
                : { value: undefined, done: true },
            ),
        }),
      }),
    );

    store.follow({ events } as unknown as QueueEvents);

    await expect.poll(() => readQueue.mock.calls.length).toBe(2);
    expect(events.mock.calls[0]?.[0].types).toContain(
      'com.prusa.connect.device.transfer.settled',
    );
    store.dispose();
  });

  it('tells a refusal from a failure', async () => {
    const refused = new PrinterQueueStore(
      apiWith({
        readQueue: () => Promise.reject(refusal(403, 'core.auth/DENIED')),
      }),
      allowing,
      'mk4-1',
    );
    await refused.refresh();
    expect(refused.load).toBe('refused');

    const failed = new PrinterQueueStore(
      apiWith({
        readQueue: () => Promise.reject(refusal(503, 'simple-queue/NOT_READY')),
      }),
      allowing,
      'mk4-1',
    );
    await failed.refresh();
    expect(failed.load).toBe('failed');
    expect(failed.fault?.code).toBe('simple-queue/NOT_READY');
  });

  it('sends each action with the printer and the entry', async () => {
    const api = apiWith({
      moveEntry: vi.fn(() => Promise.resolve(waitingQueue)),
      setReady: vi.fn(() => Promise.resolve(waitingQueue)),
      removeEntry: vi.fn(() => Promise.resolve(waitingQueue)),
    });
    const store = new PrinterQueueStore(api, allowing, 'mk4-1');

    await store.move('e2', 0);
    await store.setReady(true);
    await store.remove('e3');
    await store.setAlone(true);
    await store.setAlone(false);

    expect(api.moveEntry).toHaveBeenCalledWith(
      { path: { device: 'mk4-1', entry: 'e2' }, body: { position: 0 } },
      expect.any(AbortSignal),
    );
    expect(api.setReady).toHaveBeenCalledWith(
      { path: { device: 'mk4-1' }, body: { ready: true } },
      expect.any(AbortSignal),
    );
    expect(api.removeEntry).toHaveBeenCalledWith(
      { path: { device: 'mk4-1', entry: 'e3' } },
      expect.any(AbortSignal),
    );
    expect(allowing.allow).toHaveBeenCalledWith('mk4-1');
    expect(allowing.disallow).toHaveBeenCalledWith('mk4-1');
  });

  it('keeps a failed action above the queue until the next one', async () => {
    let refuse = true;
    const api = apiWith({
      startNext: () =>
        refuse
          ? Promise.reject(refusal(502, 'simple-queue/SEND_FAILED'))
          : Promise.resolve(sendingQueue),
    });
    const store = new PrinterQueueStore(api, allowing, 'mk4-1');

    await store.printNext();
    expect(store.actionFault?.code).toBe('simple-queue/SEND_FAILED');
    expect(store.busy).toBe(false);

    refuse = false;
    await store.printNext();
    expect(store.actionFault).toBeUndefined();
  });

  it('does not draw a read that began before an action answered', async () => {
    let finish: ((queue: Queue) => void) | undefined;
    let reads = 0;
    const api = apiWith({
      readQueue: () => {
        reads += 1;
        return reads === 1
          ? new Promise<Queue>((resolve) => {
              finish = resolve;
            })
          : Promise.resolve(waitingQueue);
      },
      moveEntry: () => Promise.resolve(waitingQueue),
    });
    const store = new PrinterQueueStore(api, allowing, 'mk4-1');

    const stale = store.refresh();
    await store.move('e2', 0);
    finish?.(emptyQueue);
    await stale;

    expect(store.queue).toBe(waitingQueue);
  });

  it('reads the queue again every few seconds until it is let go', async () => {
    vi.useFakeTimers();
    const readQueue = vi.fn(() => Promise.resolve(emptyQueue));
    const store = new PrinterQueueStore(
      apiWith({ readQueue }),
      allowing,
      'mk4-1',
    );

    store.start();
    await vi.advanceTimersByTimeAsync(REFRESH_MS * 2 + 100);
    expect(readQueue).toHaveBeenCalledTimes(3);

    store.dispose();
    await vi.advanceTimersByTimeAsync(REFRESH_MS * 2 + 100);
    expect(readQueue).toHaveBeenCalledTimes(3);
  });
});

describe('the board of every printer', () => {
  it('reads every queue in one request, however many printers there are', async () => {
    vi.useFakeTimers();
    const readBoard = vi.fn(() => Promise.resolve({ queues: someBoard }));
    const readQueue = vi.fn(() => Promise.resolve(waitingQueue));
    const board = new BoardStore(apiWith({ readBoard, readQueue }));
    board.start();
    await vi.advanceTimersByTimeAsync(0);

    expect(board.load).toBe('ready');
    expect(board.queues.map((queue) => queue.device_id)).toEqual([
      'mk4-1',
      'core-one',
      'mini',
    ]);
    await vi.advanceTimersByTimeAsync(REFRESH_MS);
    expect(readBoard).toHaveBeenCalledTimes(2);
    expect(readQueue).not.toHaveBeenCalled();

    board.dispose();
    await vi.advanceTimersByTimeAsync(REFRESH_MS * 2);
    expect(readBoard).toHaveBeenCalledTimes(2);
  });

  // An event about any printer may change what the board shows, so it reads
  // every queue again at once rather than on its timer.
  it('reads every queue again when the server says a printer changed', async () => {
    const readBoard = vi.fn(() => Promise.resolve({ queues: someBoard }));
    const board = new BoardStore(apiWith({ readBoard }));
    const frames = [
      {
        type: 'com.prusa.connect.device.state.changed',
        envelope: { source: 'urn:prusa:site:local/device/xl-2' },
      },
    ];
    let next = 0;
    const events = vi.fn<(options: { types: readonly string[] }) => unknown>(
      () => ({
        lastEventId: undefined,
        close: () => undefined,
        [Symbol.asyncIterator]: () => ({
          next: () =>
            Promise.resolve(
              next < frames.length
                ? { value: frames[next++], done: false }
                : { value: undefined, done: true },
            ),
        }),
      }),
    );

    board.follow({ events } as unknown as QueueEvents);

    await expect.poll(() => readBoard.mock.calls.length).toBe(1);
    expect(events.mock.calls[0]?.[0].types).toContain(
      'com.prusa.connect.device.state.changed',
    );
    board.dispose();
  });

  it('tells a refusal from a failure', async () => {
    const board = new BoardStore(
      apiWith({
        readBoard: () => Promise.reject(refusal(403, 'core.auth/DENIED')),
      }),
    );
    await board.refresh();
    expect(board.load).toBe('refused');
  });

  // A card dragged on the board moves its file in its own queue, and the
  // board is read again so it shows the new order.
  it('moves a file and reads the board again, keeping a refusal to show', async () => {
    const readBoard = vi.fn(() => Promise.resolve({ queues: someBoard }));
    const moveEntry = vi.fn(() => Promise.resolve(waitingQueue));
    const board = new BoardStore(apiWith({ readBoard, moveEntry }));

    await board.move('mk4-1', 'e1', 2);
    expect(moveEntry).toHaveBeenCalledWith(
      { path: { device: 'mk4-1', entry: 'e1' }, body: { position: 2 } },
      expect.any(AbortSignal),
    );
    expect(readBoard).toHaveBeenCalledTimes(1);
    expect(board.actionFault).toBeUndefined();

    moveEntry.mockImplementationOnce(() =>
      Promise.reject(refusal(403, 'core.auth/DENIED')),
    );
    await board.move('mk4-1', 'e1', 0);
    expect(board.actionFault).toBeDefined();
    expect(board.load).toBe('ready');
  });
});

describe('the board read again', () => {
  // A read that began before a move and answers after the read the move
  // asked for would draw the old order again.
  it('draws no answer older than the one it already drew', async () => {
    const old = { queues: someBoard };
    const moved = { queues: [...someBoard].reverse() };
    let answerOld: (value: { queues: typeof someBoard }) => void = () =>
      undefined;
    const readBoard = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            answerOld = resolve;
          }),
      )
      .mockImplementation(() => Promise.resolve(moved));
    const board = new BoardStore(apiWith({ readBoard }));

    const slow = board.refresh();
    await board.move('mk4-1', 'e1', 2);
    answerOld(old);
    await slow;

    expect(board.queues.map((queue) => queue.device_id)).toEqual(
      moved.queues.map((queue) => queue.device_id),
    );
  });

  // One failed read while the queues are on screen keeps them there, with
  // the fault said above them.
  it('keeps the queues it holds when a read fails', async () => {
    const readBoard = vi
      .fn()
      .mockImplementationOnce(() => Promise.resolve({ queues: someBoard }))
      .mockImplementation(() =>
        Promise.reject(refusal(502, 'core.http/BAD_GATEWAY')),
      );
    const board = new BoardStore(apiWith({ readBoard }));

    await board.refresh();
    await board.refresh();

    expect(board.load).toBe('ready');
    expect(board.queues).toHaveLength(someBoard.length);
    expect(board.fault).toBeDefined();
  });

  // An event of a printer the page does not show reads nothing, and a burst
  // of events of shown printers reads once.
  it('reads again once for a burst of events of the printers it shows', async () => {
    vi.useFakeTimers();
    const readBoard = vi.fn(() => Promise.resolve({ queues: someBoard }));
    const board = new BoardStore(apiWith({ readBoard }));
    await board.refresh();
    const shown = someBoard[0]?.device_id ?? '';
    const frames = [
      {
        type: 'com.prusa.connect.device.state.changed',
        envelope: { subject: 'device:elsewhere' },
      },
      {
        type: 'com.prusa.connect.device.state.changed',
        envelope: { subject: `device:${shown}` },
      },
      {
        type: 'com.prusa.connect.device.files.changed',
        envelope: { subject: `device:${shown}` },
      },
    ];
    let next = 0;
    const events = () => ({
      lastEventId: undefined,
      close: () => undefined,
      [Symbol.asyncIterator]: () => ({
        next: () =>
          next < frames.length
            ? Promise.resolve({ value: frames[next++], done: false })
            : new Promise(() => undefined),
      }),
    });

    board.follow({ events } as unknown as QueueEvents);
    await vi.advanceTimersByTimeAsync(WAKE_MS - 1);
    expect(readBoard).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(readBoard).toHaveBeenCalledTimes(2);
    board.dispose();
  });
});

describe('the overview the cards share', () => {
  it('reads once for every watcher, and stops after the last', async () => {
    vi.useFakeTimers();
    const readSummary = vi.fn(() => Promise.resolve(someQueues));
    const store = new OverviewStore({ readSummary });

    const first = store.watch();
    const second = store.watch();
    await vi.advanceTimersByTimeAsync(0);
    expect(readSummary).toHaveBeenCalledTimes(1);
    expect(store.of('core-one')?.waiting).toBe(1);
    expect(store.of('nobody')).toBeUndefined();

    first();
    first();
    await vi.advanceTimersByTimeAsync(REFRESH_MS + 100);
    expect(readSummary).toHaveBeenCalledTimes(2);

    second();
    await vi.advanceTimersByTimeAsync(REFRESH_MS * 2 + 100);
    expect(readSummary).toHaveBeenCalledTimes(2);
  });
});

describe('adding from the print dialog', () => {
  const file = { storage: 'local', path: 'boxes/lid-0.4n-PETG.bgcode' };

  it('says where the file waits, counted from 1', () => {
    expect(placeOf(printingQueue, file)).toEqual({
      kind: 'added',
      position: 3,
    });
  });

  it('says a file printing at once has started', () => {
    const started: Queue = {
      ...emptyQueue,
      entries: [{ ...waitingQueue.entries[2]!, state: 'sending' }],
    };

    expect(placeOf(started, file)).toEqual({ kind: 'started' });
  });

  it('says a file whose start failed did not start, not its place', () => {
    const held: Queue = {
      ...emptyQueue,
      entries: [{ ...waitingQueue.entries[2]!, note: 'send_failed' }],
    };

    expect(placeOf(held, file)).toEqual({ kind: 'held', note: 'send_failed' });
  });

  it('reads the newest copy of a file that waits twice', () => {
    const twice: Queue = {
      ...waitingQueue,
      entries: [
        ...waitingQueue.entries,
        {
          ...waitingQueue.entries[2]!,
          id: 'e4',
          added_at: '2026-10-29T09:00:00Z',
        },
      ],
    };

    expect(placeOf(twice, file)).toEqual({ kind: 'added', position: 4 });
  });

  it('turns a refusal into its words', async () => {
    const added = await addTo(
      {
        addEntry: () =>
          Promise.reject(
            Object.assign(new Error('simple-queue/QUEUE_FULL: full'), {
              status: 409,
              code: 'simple-queue/QUEUE_FULL',
              message: 'the queue of this printer is full',
            }),
          ),
      },
      'mk4-1',
      file,
    );

    expect(added.kind).toBe('failed');
  });
});

describe('adding a file that lies on the printer', () => {
  it('names no storage in the body', () => {
    expect(bodyOf({ path: 'usb/a.bgcode' })).toEqual({ path: 'usb/a.bgcode' });
    expect(bodyOf({ storage: 'local', path: 'a.bgcode' })).toEqual({
      storage: 'local',
      path: 'a.bgcode',
    });
  });

  it('finds where it waits by its empty storage', () => {
    const queue: Queue = {
      ...waitingQueue,
      entries: [
        ...waitingQueue.entries,
        {
          ...waitingQueue.entries[0],
          id: 'p',
          storage: '',
          path: 'usb/a.bgcode',
        },
      ] as Queue['entries'],
    };

    expect(placeOf(queue, { path: 'usb/a.bgcode' })).toEqual({
      kind: 'added',
      position: 4,
    });
  });

  it('adds each file in turn and counts the ones refused', async () => {
    const addEntry = vi.fn(({ body }: { body: { path: string } }) =>
      body.path === 'usb/full.bgcode'
        ? Promise.reject(refusal(409, 'simple-queue/QUEUE_FULL'))
        : Promise.resolve(waitingQueue),
    );
    const store = new PrinterQueueStore(
      apiWith({ addEntry }),
      allowing,
      'mk4-1',
    );

    const done = await store.addFiles([
      { path: 'usb/a.bgcode' },
      { path: 'usb/full.bgcode' },
      { storage: 'local', path: 'b.bgcode' },
    ]);

    expect(done.added).toBe(2);
    expect(done.failed.map((one) => one.name)).toEqual(['full.bgcode']);
    expect(addEntry.mock.calls.map(([call]) => call.body)).toEqual([
      { path: 'usb/a.bgcode' },
      { path: 'usb/full.bgcode' },
      { storage: 'local', path: 'b.bgcode' },
    ]);
  });
});

describe('the file picker', () => {
  it('lists every print file of the printer, whatever folder it is in', async () => {
    const picker = new FilePicker(fakeFilesApi(), 'mk4-1');
    await picker.start();

    expect(picker.places.map((place) => place.kind)).toEqual([
      'printer',
      'storage',
    ]);
    expect(picker.root).toBe('usb');
    expect(picker.rows.every((row) => !row.folder)).toBe(true);
    expect(picker.rows.map((row) => row.name)).toContain('benchy.bgcode');
    expect(picker.rows.some((row) => row.where?.startsWith('usb/models'))).toBe(
      true,
    );
  });

  it('opens a printer with one storage on that storage in the folder view', async () => {
    const picker = new FilePicker(fakeFilesApi(), 'mk4-1');
    await picker.start();
    await picker.show('folders');

    expect(picker.directory).toBe('usb');
    expect(picker.rows.map((row) => row.name)).toEqual([
      'models',
      'benchy.bgcode',
      'bracket-0.4n-PETG.bgcode',
    ]);
  });

  it('waits for a printer that is still reading its drive', async () => {
    vi.useFakeTimers();
    try {
      const files = fakeFilesApi();
      let asked = 0;
      const picker = new FilePicker(
        {
          ...files,
          listDeviceFiles: (request, signal) =>
            files
              .listDeviceFiles(request, signal)
              .then((listed) =>
                asked++ < 2 ? { ...listed, items: [], reading: true } : listed,
              ),
        },
        'mk4-1',
      );
      const started = picker.start();
      await vi.runAllTimersAsync();
      await started;

      expect(picker.rows.map((row) => row.name)).toContain('benchy.bgcode');
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps a tick while the person moves to another place', async () => {
    const picker = new FilePicker(fakeFilesApi(), 'mk4-1');
    await picker.start();
    const benchy = picker.rows.find((row) => row.name === 'benchy.bgcode');
    if (benchy === undefined) throw new Error('no benchy');
    picker.toggle(benchy);
    const local = picker.places[1];
    if (local === undefined) throw new Error('no storage');
    await picker.choose(local);
    const lid = picker.rows.find((row) => row.name === 'lid-0.4n-PETG.bgcode');
    if (lid === undefined) throw new Error('no file');
    picker.toggle(lid);

    expect(picker.picked.map((one) => one.file)).toEqual([
      { path: 'usb/benchy.bgcode' },
      { storage: 'local', path: 'lid-0.4n-PETG.bgcode' },
    ]);
    picker.toggle(lid);
    expect(picker.picked).toHaveLength(1);
  });
});

describe('the grant that lets the queue start prints', () => {
  it('is written to and withdrawn from the instance the host names', async () => {
    const written: unknown[] = [];
    const client = {
      writeGrant: (call: unknown) => {
        written.push(call);
        return Promise.resolve({});
      },
      withdrawGrant: (call: unknown) => {
        written.push(call);
        return Promise.resolve({});
      },
    } as unknown as ModuleHost['client'];
    const grants = grantsOn({ client, self: 'sink:queue' });

    await grants.allow('p1');
    await grants.disallow('p1');

    expect(written).toEqual([
      {
        path: { object_type: 'device', object_id: 'p1' },
        body: { relation: 'operator', subject: 'sink:queue' },
      },
      {
        path: { object_type: 'device', object_id: 'p1' },
        query: { relation: 'operator', subject: 'sink:queue' },
      },
    ]);
  });
});
