import type { StreamEvent } from '@prusa/connect';
import { describe, expect, it, vi } from 'vitest';

import {
  createRunningTransfers,
  type RunningTransferRow,
  runningTransferOf,
} from './running-transfers.svelte';
import type {
  RunningTransfer,
  RunningTransfersClient,
} from './services/transfers';
import { hubEvent, StubApiError } from './stubs';

const runningOf = (
  transferId: string,
  overrides: Partial<RunningTransfer> = {},
): RunningTransfer => ({
  transfer_id: transferId,
  device_id: 'printer-1',
  printer_name: 'Workshop MK4',
  storage: 'local',
  source: `${transferId}.bgcode`,
  path: `usb/${transferId}.bgcode`,
  bytes_total: 1024,
  bytes_sent: 256,
  state: 'sending',
  started_at: '2025-09-04T15:33:20Z',
  mine: false,
  can_stop: false,
  ...overrides,
});

interface Calls {
  list: number;
  stopped: { device: string; transfer: string }[];
}

const stubClient = (
  answers: (readonly RunningTransfer[])[],
  refuseStop?: StubApiError,
): { client: RunningTransfersClient; calls: Calls } => {
  const calls: Calls = { list: 0, stopped: [] };
  const client: RunningTransfersClient = {
    listRunningTransfers: () => {
      const answer = answers[Math.min(calls.list, answers.length - 1)] ?? [];
      calls.list += 1;

      return Promise.resolve({ items: [...answer] });
    },
    stopDeviceTransfer: (request) => {
      calls.stopped.push({
        device: request.path.device_id,
        transfer: request.path.transfer_id,
      });
      if (refuseStop !== undefined) return Promise.reject(refuseStop);

      return Promise.resolve();
    },
  };

  return { client, calls };
};

const started = (transferId: string, device = 'printer-1'): StreamEvent =>
  hubEvent(
    'com.prusa.connect.device.transfer.started',
    {
      transfer_id: transferId,
      storage: 'local',
      source: `${transferId}.bgcode`,
      destination: `usb/${transferId}.bgcode`,
      bytes_total: 1024,
    },
    `device:${device}`,
  );

const progressed = (transferId: string, bytesSent: number): StreamEvent =>
  hubEvent(
    'com.prusa.connect.device.transfer.progressed',
    { transfer_id: transferId, bytes_total: 1024, bytes_sent: bytesSent },
    'device:printer-1',
  );

const settled = (transferId: string, state = 'done'): StreamEvent =>
  hubEvent(
    'com.prusa.connect.device.transfer.settled',
    { transfer_id: transferId, state, bytes_sent: 1024 },
    'device:printer-1',
  );

const LOST = {
  type: 'com.prusa.connect.stream.continuity.lost',
  id: '9',
  payload: { cursor: '9', dropped: 3 },
  envelope: undefined,
} as const;

describe('RunningTransfers', () => {
  it('reads every running transfer with who started it and what the person may do', async () => {
    const { client } = stubClient([
      [
        runningOf('t-mine', { mine: true, can_stop: true }),
        runningOf('t-theirs', {
          device_id: 'printer-2',
          printer_name: 'Shop XL',
          started_by: 'user:ada',
        }),
      ],
    ]);
    const store = createRunningTransfers(client);

    expect(store.loaded).toBe(false);
    await store.load();

    expect(store.loaded).toBe(true);
    expect(store.rows.map((row) => row.transferId)).toEqual([
      't-mine',
      't-theirs',
    ]);
    expect(store.transfer('t-mine')).toMatchObject({
      mine: true,
      canStop: true,
      startedBy: undefined,
      printerName: 'Workshop MK4',
    });
    expect(store.transfer('t-theirs')).toMatchObject({
      mine: false,
      canStop: false,
      startedBy: 'user:ada',
      deviceId: 'printer-2',
    });
    expect(store.progress('t-mine')).toBe(0.25);
  });

  it('costs one read for two loads at once', async () => {
    const { client, calls } = stubClient([[runningOf('t-1')]]);
    const store = createRunningTransfers(client);

    await Promise.all([store.load(), store.load()]);

    expect(calls.list).toBe(1);
  });

  it('reads the listing for a transfer it hears start, and moves its bytes from the frames after', async () => {
    const { client, calls } = stubClient([[], [runningOf('t-new')]]);
    const store = createRunningTransfers(client);
    await store.load();

    await store.apply(started('t-new'));
    expect(calls.list).toBe(2);
    expect(store.rows.map((row) => row.transferId)).toEqual(['t-new']);

    await store.apply(progressed('t-new', 768));
    expect(store.progress('t-new')).toBe(0.75);
    expect(calls.list).toBe(2);
  });

  it('drops a transfer the moment it settles', async () => {
    const { client } = stubClient([[runningOf('t-1'), runningOf('t-2')]]);
    const store = createRunningTransfers(client);
    await store.load();

    await store.apply(settled('t-1'));

    expect(store.rows.map((row) => row.transferId)).toEqual(['t-2']);
  });

  it('remembers which files landed whole, until a send forgets one', async () => {
    const { client } = stubClient([[runningOf('t-1'), runningOf('t-2')]]);
    const store = createRunningTransfers(client);
    await store.load();
    const first = store.transfer('t-1');
    const second = store.transfer('t-2');
    if (first) store.awaitLanding(first);
    if (second) store.awaitLanding(second);

    await store.apply(settled('t-1'));
    await store.apply(settled('t-2', 'failed'));

    expect(first && store.landedAt(first)).toBe(true);
    expect(second && store.landedAt(second)).toBe(false);
    if (first) store.forget(first);
    expect(first && store.landedAt(first)).toBe(false);
  });

  it('does not tell a wait that was taken back, for a send that started no transfer', async () => {
    const { client } = stubClient([[], [runningOf('t-new')]]);
    const store = createRunningTransfers(client);
    await store.load();
    const going = vi.fn();
    const sent = {
      deviceId: 'printer-1',
      storage: 'local',
      source: 't-new.bgcode',
    };
    store.whenOnItsWay(sent, going);
    store.notOnItsWay(sent);

    await store.apply(started('t-new'));

    expect(going).not.toHaveBeenCalled();
  });

  it('tells who waits for a file where it lands once it is on its way, and not after a failure', async () => {
    const { client } = stubClient([[], [runningOf('t-new')], []]);
    const store = createRunningTransfers(client);
    await store.load();
    const going = vi.fn();
    const failing = vi.fn();
    store.whenOnItsWay(
      { deviceId: 'printer-1', storage: 'local', source: 't-new.bgcode' },
      going,
    );
    store.whenOnItsWay(
      { deviceId: 'printer-1', storage: 'local', source: 't-bad.bgcode' },
      failing,
    );

    await store.apply(started('t-new'));
    await store.apply(progressed('t-new', 512));
    expect(going).toHaveBeenCalledExactlyOnceWith('usb/t-new.bgcode');

    await store.apply({
      ...started('t-bad'),
      type: 'com.prusa.connect.device.transfer.settled',
      payload: {
        transfer_id: 't-bad',
        state: 'failed',
        storage: 'local',
        source: 't-bad.bgcode',
        destination: 'usb/t-bad.bgcode',
      },
    } as StreamEvent);
    expect(failing).not.toHaveBeenCalled();
  });

  it('reads what a transfer moved from its settled frame when it held no row', async () => {
    const { client } = stubClient([[]]);
    const store = createRunningTransfers(client);
    await store.load();
    const quick = {
      deviceId: 'printer-1',
      storage: 'local',
      source: 'quick.bgcode',
      path: 'usb/quick.bgcode',
    };
    store.awaitLanding(quick);

    await store.apply(
      hubEvent(
        'com.prusa.connect.device.transfer.settled',
        {
          transfer_id: 't-quick',
          state: 'done',
          storage: 'local',
          source: 'quick.bgcode',
          destination: 'usb/quick.bgcode',
        },
        'device:printer-1',
      ),
    );

    expect(store.landedAt(quick)).toBe(true);
  });

  it('keeps no landing nobody waits for', async () => {
    const { client } = stubClient([[runningOf('t-1')]]);
    const store = createRunningTransfers(client);
    await store.load();
    const moved = store.transfer('t-1');

    await store.apply(settled('t-1'));

    expect(moved && store.landedAt(moved)).toBe(false);
  });

  it('keeps a settled transfer out when a read that left before the frame still lists it', async () => {
    const { client } = stubClient([[runningOf('t-1')], [runningOf('t-1')]]);
    const store = createRunningTransfers(client);
    await store.load();

    await store.apply(settled('t-1'));
    await store.apply(LOST);

    expect(store.rows).toEqual([]);
  });

  it('reads an unknown transfer once, however many frames name it', async () => {
    const { client, calls } = stubClient([[]]);
    const store = createRunningTransfers(client);
    await store.load();

    await store.apply(progressed('t-elsewhere', 100));
    await store.apply(progressed('t-elsewhere', 200));
    await store.apply(progressed('t-elsewhere', 300));

    expect(calls.list).toBe(2);
    expect(store.rows).toEqual([]);
  });

  it('reads again after a start heard while a read was in flight', async () => {
    const { client, calls } = stubClient([[], [runningOf('t-late')]]);
    const store = createRunningTransfers(client);

    const first = store.load();
    void store.apply(started('t-late'));
    await first;

    expect(calls.list).toBe(2);
    expect(store.rows.map((row) => row.transferId)).toEqual(['t-late']);
  });

  it('reads again when the stream lost its place', async () => {
    const { client, calls } = stubClient([[], [runningOf('t-gap')]]);
    const store = createRunningTransfers(client);
    await store.load();

    await store.apply(LOST);

    expect(calls.list).toBe(2);
    expect(store.rows.map((row) => row.transferId)).toEqual(['t-gap']);
  });

  it('ignores an event that is not about a transfer', async () => {
    const { client, calls } = stubClient([[]]);
    const store = createRunningTransfers(client);
    await store.load();

    await store.apply(
      hubEvent(
        'com.prusa.connect.device.record.changed',
        { name: 'x' },
        'device:printer-1',
      ),
    );

    expect(calls.list).toBe(1);
  });

  it('stops a transfer on the printer it goes to', async () => {
    const { client, calls } = stubClient([
      [runningOf('t-1', { device_id: 'printer-7', can_stop: true })],
    ]);
    const store = createRunningTransfers(client);
    await store.load();

    const stopping = store.stop('t-1');
    expect(store.stopping('t-1')).toBe(true);
    await stopping;

    expect(calls.stopped).toEqual([{ device: 'printer-7', transfer: 't-1' }]);
    expect(store.stopping('t-1')).toBe(false);
    // The row stays until the settled frame says the transfer ended.
    expect(store.rows).toHaveLength(1);
  });

  it('keeps the refusal of a stop', async () => {
    const { client } = stubClient(
      [[runningOf('t-1')]],
      new StubApiError('ACCESS_DENIED'),
    );
    const store = createRunningTransfers(client);
    await store.load();

    await store.stop('t-1');

    expect(store.error?.code).toBe('ACCESS_DENIED');
  });

  it('takes the transfer events the fleet passes on', async () => {
    const { client } = stubClient([[runningOf('t-1')]]);
    const store = createRunningTransfers(client);
    await store.load();
    let listener: ((event: StreamEvent) => void) | undefined;
    const stop = store.watch({
      subscribe: (heard) => {
        listener = heard;

        return () => {
          listener = undefined;
        };
      },
    });

    listener?.(settled('t-1'));
    await Promise.resolve();
    expect(store.rows).toEqual([]);

    stop();
    expect(listener).toBeUndefined();
  });

  it('forgets every row when the person changes, and drops a read on its way', async () => {
    const { client } = stubClient([[runningOf('t-1')]]);
    const store = createRunningTransfers(client);
    await store.load();

    const late = store.load();
    store.reset();
    await late;

    expect(store.rows).toEqual([]);
    expect(store.loaded).toBe(false);
  });
});

describe('runningTransferOf', () => {
  const rowOf = (
    transferId: string,
    path: string,
    deviceId = 'printer-1',
  ): RunningTransferRow => ({
    transferId,
    deviceId,
    printerName: 'Workshop MK4',
    storage: 'local',
    source: 'models/bracket.bgcode',
    path,
    bytesTotal: 1024,
    bytesSent: 256,
    state: 'sending',
    mine: true,
    startedBy: undefined,
    canStop: true,
  });
  const rows = [
    rowOf('newest', 'usb/bracket.bgcode'),
    rowOf('older', 'usb/copy/bracket.bgcode'),
    rowOf('elsewhere', 'usb/bracket.bgcode', 'printer-2'),
  ];
  const file = {
    deviceId: 'printer-1',
    storage: 'local',
    source: 'models/bracket.bgcode',
  };

  it('answers the newest transfer of the file to the printer', () => {
    expect(runningTransferOf(rows, file)?.transferId).toBe('newest');
    expect(
      runningTransferOf(rows, { ...file, deviceId: 'printer-2' })?.transferId,
    ).toBe('elsewhere');
  });

  it('matches the landing only when one is named', () => {
    expect(
      runningTransferOf(rows, { ...file, path: 'usb/copy/bracket.bgcode' })
        ?.transferId,
    ).toBe('older');
    expect(
      runningTransferOf(rows, { ...file, source: 'models/other.bgcode' }),
    ).toBeUndefined();
  });
});
