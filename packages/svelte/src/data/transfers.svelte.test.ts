import type { StreamEvent } from '@prusa/connect';
import { describe, expect, it, vi } from 'vitest';

import type { Transfer, TransfersClient } from './services/transfers';
import {
  fakeEvents,
  fakeStream,
  hubEvent,
  noEvents,
  StubApiError,
} from './stubs';
import { createTransfers } from './transfers.svelte';

const transferOf = (
  transferId: string,
  state = 'sending',
  bytesSent = 512,
): Transfer => ({
  bytes_sent: bytesSent,
  bytes_total: 1024,
  path: `usb/${transferId}.bgcode`,
  source: `${transferId}.bgcode`,
  started_at: '2025-09-04T15:33:20Z',
  state,
  storage: 'local',
  transfer_id: transferId,
});

interface Calls {
  list: number;
  stopped: string[];
  sent: string[];
}

const stubClient = (
  rows: readonly Transfer[],
  events = noEvents,
  refuse?: StubApiError,
): { client: TransfersClient; calls: Calls } => {
  const calls: Calls = { list: 0, stopped: [], sent: [] };
  const held = [...rows];
  const client: TransfersClient = {
    listDeviceTransfers: () => {
      calls.list += 1;

      return Promise.resolve({ items: [...held] });
    },
    stopDeviceTransfer: (request) => {
      calls.stopped.push(request.path.transfer_id);
      const at = held.findIndex(
        (row) => row.transfer_id === request.path.transfer_id,
      );
      const found = held[at];
      if (found !== undefined) held[at] = { ...found, state: 'cancelled' };

      return Promise.resolve();
    },
    sendDeviceFile: (request) => {
      if (refuse) throw refuse;
      calls.sent.push(request.body.source);
      const made = transferOf('t-new', 'queued', 0);

      return Promise.resolve({ path: request.body.path, transfer: made });
    },
    printDeviceFile: (request) => {
      if (refuse) throw refuse;
      calls.sent.push(request.body.source ?? '');

      return Promise.resolve({
        path: request.body.path,
        command: { command_id: 'c-1', ok: true },
        transfer: transferOf('t-new', 'done', 10),
      });
    },
    events,
  };

  return { client, calls };
};

const started = (transferId: string): StreamEvent =>
  hubEvent(
    'com.prusa.connect.device.transfer.started',
    {
      transfer_id: transferId,
      storage: 'local',
      source: `${transferId}.bgcode`,
      destination: `usb/${transferId}.bgcode`,
      bytes_total: 1024,
    },
    'device:printer-1',
  );

const progressed = (transferId: string, bytesSent: number): StreamEvent =>
  hubEvent(
    'com.prusa.connect.device.transfer.progressed',
    { transfer_id: transferId, bytes_total: 1024, bytes_sent: bytesSent },
    'device:printer-1',
  );

const settled = (transferId: string, state: string): StreamEvent =>
  hubEvent(
    'com.prusa.connect.device.transfer.settled',
    {
      transfer_id: transferId,
      state,
      bytes_sent: 1024,
      bytes_total: 1024,
      reason: state === 'done' ? '' : 'the printer went away',
    },
    'device:printer-1',
  );

const LOST = {
  type: 'com.prusa.connect.stream.continuity.lost',
  id: '9',
  payload: { cursor: '9', dropped: 3 },
  envelope: undefined,
} as const;

// A settled frame the way the hub writes one: the origin is the subject and
// the last part of the source, and the payload names no device.
const settledOn = (
  printer: string,
  transferId: string,
  shape: { subject: boolean } = { subject: true },
): StreamEvent => ({
  type: 'com.prusa.connect.device.transfer.settled',
  id: '7',
  payload: {
    transfer_id: transferId,
    state: 'done',
    destination: `usb/${transferId}.bgcode`,
    bytes_sent: 1024,
    bytes_total: 1024,
  },
  envelope: {
    specversion: '1.0',
    id: '7',
    source: `urn:prusa:site:site-1/device/${printer}`,
    type: 'com.prusa.connect.device.transfer.settled',
    time: '2026-09-12T00:00:00Z',
    ...(shape.subject ? { subject: `device:${printer}` } : {}),
    hops: 0,
    evclass: 'state',
  },
});

describe('Transfers', () => {
  it('starts unread, which is not the same as a printer sending nothing', () => {
    const { client } = stubClient([]);
    const store = createTransfers(client, 'printer-1');

    expect(store.rows).toEqual([]);
    expect(store.loaded).toBe(false);
    expect(store.deviceId).toBe('printer-1');
  });

  it('reads the transfers and says which are still moving bytes', async () => {
    const { client } = stubClient([
      transferOf('t-1', 'sending'),
      transferOf('t-2', 'done', 1024),
    ]);
    const store = createTransfers(client, 'printer-1');

    await store.load();

    expect(store.loaded).toBe(true);
    expect(store.inFlight.map((row) => row.transferId)).toEqual(['t-1']);
    expect(store.progress('t-1')).toBe(0.5);
  });

  // svelte-data.md: two readers of the same list cost one request.
  it('costs one request however many readers ask at once', async () => {
    const { client, calls } = stubClient([transferOf('t-1')]);
    const store = createTransfers(client, 'printer-1');

    await Promise.all([store.load(), store.load()]);

    expect(calls.list).toBe(1);
  });

  it('opens a transfer from a started frame it never listed', async () => {
    const { client } = stubClient([]);
    const store = createTransfers(client, 'printer-1');

    await store.apply(started('t-9'));

    expect(store.rows).toHaveLength(1);
    expect(store.rows[0]?.state).toBe('sending');
    expect(store.rows[0]?.path).toBe('usb/t-9.bgcode');
  });

  // The listing is newest first, and a transfer that starts later is newer
  // than every row the listing answered.
  it('puts a transfer it learns of from a started frame first', async () => {
    const { client } = stubClient([transferOf('t-1', 'done')]);
    const store = createTransfers(client, 'printer-1');
    await store.load();

    await store.apply(started('t-9'));

    expect(store.rows.map((row) => row.transferId)).toEqual(['t-9', 't-1']);
  });

  it('puts a transfer it sent itself first', async () => {
    const { client } = stubClient([transferOf('t-1', 'done')]);
    const store = createTransfers(client, 'printer-1');
    await store.load();

    await store.send('local', 'cube.bgcode', 'usb/cube.bgcode');

    expect(store.rows.map((row) => row.transferId)).toEqual(['t-new', 't-1']);
  });

  it('moves the bytes on a progress frame and leaves the row in place', async () => {
    const { client } = stubClient([transferOf('t-1', 'sending', 0)]);
    const store = createTransfers(client, 'printer-1');
    await store.load();
    const before = store.rows;

    await store.apply(progressed('t-1', 768));

    expect(store.rows).toHaveLength(1);
    expect(store.progress('t-1')).toBe(0.75);
    // The collection is replaced whole, which is one recomputation and not one
    // per row (svelte-5.md).
    expect(store.rows).not.toBe(before);
  });

  it('closes a transfer on a settled frame, with the reason it ended', async () => {
    const { client } = stubClient([transferOf('t-1')]);
    const store = createTransfers(client, 'printer-1');
    await store.load();

    await store.apply(settled('t-1', 'failed'));

    expect(store.rows[0]?.state).toBe('failed');
    expect(store.rows[0]?.reason).toBe('the printer went away');
    expect(store.inFlight).toEqual([]);
  });

  // The listing is the printer's whole history: a transfer that failed before
  // the store was built would otherwise sit in the panel for good.
  it('counts as current only the transfers it saw moving', async () => {
    const { client } = stubClient([
      transferOf('t-1', 'sending'),
      transferOf('t-old', 'cancelled'),
    ]);
    const store = createTransfers(client, 'printer-1');
    await store.load();

    await store.apply(settled('t-1', 'failed'));
    await store.apply(started('t-2'));

    expect(store.current.map((row) => row.transferId)).toEqual(['t-2', 't-1']);
    expect(store.rows).toHaveLength(3);
  });

  // A file that lands changes a folder on the printer, and the page reads it
  // again from here rather than from an effect watching the rows.
  it('says where a file landed when its transfer settles done', async () => {
    const { client } = stubClient([transferOf('t-1')]);
    const landed: string[] = [];
    const store = createTransfers(client, 'printer-1');
    store.onLanded((path) => {
      landed.push(path);
    });
    await store.load();

    await store.apply(progressed('t-1', 768));
    await store.apply(settled('t-1', 'failed'));
    expect(landed).toEqual([]);

    await store.apply(started('t-2'));
    await store.apply(settled('t-2', 'done'));
    await store.apply(settled('t-2', 'done'));

    expect(landed).toEqual(['usb/t-2.bgcode']);
  });

  // The server answers a send only after the transfer settled, so the done row
  // can arrive in the response before the stream's settled frame.
  it('says a file landed once when the send answers done before the stream', async () => {
    const { client } = stubClient([]);
    client.sendDeviceFile = (request) =>
      Promise.resolve({
        path: request.body.path,
        transfer: transferOf('t-new', 'done', 1024),
      });
    const landed: string[] = [];
    const store = createTransfers(client, 'printer-1');
    store.onLanded((path) => {
      landed.push(path);
    });

    await store.send('local', 't-new.bgcode', 'usb/t-new.bgcode');
    await store.apply(settled('t-new', 'done'));

    expect(landed).toEqual(['usb/t-new.bgcode']);
  });

  // The started frame can trail the answer that already said done. It must not
  // reopen the row, or the settled frame after it ends the transfer a second
  // time and the person is told twice.
  it('keeps a done row done when a started frame arrives after the answer', async () => {
    const { client } = stubClient([]);
    client.sendDeviceFile = (request) =>
      Promise.resolve({
        path: request.body.path,
        transfer: transferOf('t-new', 'done', 1024),
      });
    const landed: string[] = [];
    const store = createTransfers(client, 'printer-1');
    store.onLanded((path) => {
      landed.push(path);
    });

    await store.send('local', 't-new.bgcode', 'usb/t-new.bgcode');
    await store.apply(started('t-new'));
    expect(store.transfer('t-new')?.state).toBe('done');
    await store.apply(settled('t-new', 'done'));

    expect(landed).toEqual(['usb/t-new.bgcode']);
  });

  // The stream is asked for this printer's frames, and a frame about another
  // printer that arrives anyway changes nothing here.
  it('subscribes to this printer and ignores a frame about another', async () => {
    const asked: unknown[] = [];
    const stream = fakeStream();
    const events = ((filter: unknown) => {
      asked.push(filter);

      return stream.subscription;
    }) as unknown as TransfersClient['events'];
    const landed: string[] = [];
    const { client } = stubClient([], events);
    const store = createTransfers(client, 'printer-1');
    store.onLanded((path) => {
      landed.push(path);
    });

    const close = store.listen();
    expect(asked).toEqual([
      expect.objectContaining({ origins: ['device:printer-1'] }),
    ]);

    await store.apply(settledOn('printer-2', 't-9'));
    // A frame relayed from a server below carries its origin in the source
    // alone, and it is read from there.
    await store.apply(settledOn('printer-2', 't-8', { subject: false }));

    expect(store.rows).toEqual([]);
    expect(landed).toEqual([]);

    await store.apply(settledOn('printer-1', 't-7', { subject: false }));
    expect(landed).toEqual(['usb/t-7.bgcode']);
    close();
  });

  // A lost place before the first listing has no earlier rows to compare
  // with, so every done row in the listing is history and not a landing.
  it('reports no landing for a lost place before the first listing', async () => {
    const { client } = stubClient([transferOf('t-1', 'done')]);
    const landed: string[] = [];
    const store = createTransfers(client, 'printer-1');
    store.onLanded((path) => {
      landed.push(path);
    });

    await store.apply(LOST);

    expect(store.rows).toHaveLength(1);
    expect(landed).toEqual([]);
  });

  // A read already in flight may have left before the transfer landed, so a
  // lost place asks for a read of its own.
  it('reads again after a lost place even while a read is in flight', async () => {
    const answers: ((items: Transfer[]) => void)[] = [];
    const client: TransfersClient = {
      ...stubClient([]).client,
      listDeviceTransfers: () =>
        new Promise((resolve) => {
          answers.push((items) => {
            resolve({ items });
          });
        }),
    };
    const store = createTransfers(client, 'printer-1');

    const first = store.load();
    const lost = store.apply(LOST);
    await vi.waitFor(() => {
      expect(answers).toHaveLength(2);
    });
    answers[1]?.([transferOf('t-1', 'done')]);
    answers[0]?.([]);
    await Promise.all([first, lost]);

    // The older answer came last and does not replace the newer one.
    expect(store.rows.map((row) => row.state)).toEqual(['done']);
  });

  // A page waits on its own read before it takes the rows as history. A read
  // that a newer one replaced must answer only once the rows are the newer
  // read's, or the page takes stale rows as its baseline.
  it('answers a replaced read only with the rows of the read that replaced it', async () => {
    const answers: ((items: Transfer[]) => void)[] = [];
    const client: TransfersClient = {
      ...stubClient([]).client,
      listDeviceTransfers: () =>
        new Promise((resolve) => {
          answers.push((items) => {
            resolve({ items });
          });
        }),
    };
    const store = createTransfers(client, 'printer-1');

    let answered = false;
    const first = store.load().then(() => {
      answered = true;
    });
    const lost = store.apply(LOST);
    await vi.waitFor(() => {
      expect(answers).toHaveLength(2);
    });

    answers[0]?.([]);
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(answered).toBe(false);

    answers[1]?.([transferOf('t-1', 'done')]);
    await Promise.all([first, lost]);
    expect(answered).toBe(true);
    expect(store.rows.map((row) => row.transferId)).toEqual(['t-1']);
  });

  // Two pages share one store. The one that closes first must not cut the
  // other off the stream.
  it('keeps the stream open until the last listener closes', () => {
    const stream = fakeStream();
    let opened = 0;
    const events = (() => {
      opened += 1;

      return stream.subscription;
    }) as unknown as TransfersClient['events'];
    const closed = vi.spyOn(stream.subscription, 'close');
    const { client } = stubClient([], events);
    const store = createTransfers(client, 'printer-1');

    const first = store.listen();
    const second = store.listen();
    expect(opened).toBe(1);

    first();
    expect(closed).not.toHaveBeenCalled();
    second();
    expect(closed).toHaveBeenCalledTimes(1);
  });

  // A file that landed while the stream was cut is only seen in the listing
  // read after it, and its folder still has to be read again.
  it('says where a file landed while the stream was cut', async () => {
    const rows = [transferOf('t-1', 'sending'), transferOf('t-2', 'done')];
    let reads = 0;
    const client: TransfersClient = {
      ...stubClient([]).client,
      listDeviceTransfers: () => {
        reads += 1;

        return Promise.resolve({ items: [...rows] });
      },
    };
    const landed: string[] = [];
    const store = createTransfers(client, 'printer-1');
    store.onLanded((path) => {
      landed.push(path);
    });
    await store.load();
    expect(landed).toEqual([]);

    rows[0] = transferOf('t-1', 'done');
    await store.apply(LOST);

    expect(reads).toBe(2);
    expect(landed).toEqual(['usb/t-1.bgcode']);
  });

  // The store is shared by several pages, and a page that closes takes its
  // listener with it.
  it('stops calling a listener once it is removed', async () => {
    const { client } = stubClient([]);
    const store = createTransfers(client, 'printer-1');
    const landed: string[] = [];
    const remove = store.onLanded((path) => {
      landed.push(path);
    });

    remove();
    await store.apply(started('t-3'));
    await store.apply(settled('t-3', 'done'));

    expect(landed).toEqual([]);
  });

  it('ignores a frame that names no transfer', async () => {
    const { client } = stubClient([]);
    const store = createTransfers(client, 'printer-1');

    await store.apply(
      hubEvent('com.prusa.connect.device.transfer.progressed', {}),
    );

    expect(store.rows).toEqual([]);
  });

  it('reads the transfers again when its place in the log is gone', async () => {
    const { client, calls } = stubClient([transferOf('t-1')]);
    const store = createTransfers(client, 'printer-1');

    await store.apply({
      type: 'com.prusa.connect.stream.continuity.lost',
      id: '9',
      payload: { cursor: '9', dropped: 3 },
      envelope: undefined,
    });

    expect(calls.list).toBe(1);
    expect(store.loaded).toBe(true);
  });

  it('sends a file and keeps the transfer the answer named', async () => {
    const { client, calls } = stubClient([]);
    const store = createTransfers(client, 'printer-1');

    const answered = await store.send(
      'local',
      'cube.bgcode',
      'usb/cube.bgcode',
    );

    expect(calls.sent).toEqual(['cube.bgcode']);
    expect(answered?.transfer.transfer_id).toBe('t-new');
    expect(store.rows.map((row) => row.transferId)).toEqual(['t-new']);
  });

  it('reports a refusal by its code and never as a sentence', async () => {
    const { client } = stubClient(
      [],
      noEvents,
      new StubApiError('core.device/STORAGE_FULL', 507),
    );
    const store = createTransfers(client, 'printer-1');

    await store.send('local', 'cube.bgcode', 'usb/cube.bgcode');

    expect(store.error?.code).toBe('core.device/STORAGE_FULL');
    expect(store.error?.status).toBe(507);
  });

  it('asks about each name the printer holds instead of failing', async () => {
    const bodies: { source: string; replace?: boolean | undefined }[] = [];
    const { client } = stubClient([]);
    client.sendDeviceFile = (request) => {
      bodies.push({
        source: request.body.source,
        replace: request.body.replace,
      });
      if (request.body.replace !== true) {
        throw new StubApiError('core.printer/FILE_NAME_TAKEN', 409);
      }

      return Promise.resolve({
        path: request.body.path,
        transfer: transferOf('t-new', 'queued', 0),
      });
    };
    const store = createTransfers(client, 'printer-1');

    await store.send('local', 'a.bgcode', 'usb/a.bgcode');
    await store.send('local', 'b.bgcode', 'usb/b.bgcode');

    expect(store.error).toBeUndefined();
    expect(store.taken?.path).toBe('usb/a.bgcode');
    expect(store.waiting.map((one) => one.path)).toEqual([
      'usb/a.bgcode',
      'usb/b.bgcode',
    ]);

    await store.replaceTaken();
    expect(bodies.at(-1)).toEqual({ source: 'a.bgcode', replace: true });
    expect(store.taken?.path).toBe('usb/b.bgcode');
    expect(store.waiting.map((one) => one.path)).toEqual(['usb/b.bgcode']);

    store.keepTaken();
    expect(store.taken).toBeUndefined();
    expect(store.waiting).toEqual([]);
    expect(bodies).toHaveLength(3);
  });

  it('answers the fault of a replace the server refused', async () => {
    const { client } = stubClient([]);
    client.sendDeviceFile = (request) => {
      throw new StubApiError(
        request.body.replace === true
          ? 'core.printer/STORAGE_REFUSED'
          : 'core.printer/FILE_NAME_TAKEN',
        409,
      );
    };
    const store = createTransfers(client, 'printer-1');
    await store.send('local', 'a.bgcode', 'usb/a.bgcode');

    const ended = await store.replaceTaken();

    expect(ended?.fault?.code).toBe('core.printer/STORAGE_REFUSED');
    expect(ended?.answer).toBeUndefined();
  });

  it('drops the question at once and sends the replacing file in its turn', async () => {
    const bodies: { source: string; replace?: boolean | undefined }[] = [];
    const { client } = stubClient([]);
    client.sendDeviceFile = (request) => {
      bodies.push({
        source: request.body.source,
        replace: request.body.replace,
      });
      if (request.body.replace !== true) {
        throw new StubApiError('core.printer/FILE_NAME_TAKEN', 409);
      }

      return Promise.resolve({
        path: request.body.path,
        transfer: transferOf('t-new', 'queued', 0),
      });
    };
    const store = createTransfers(client, 'printer-1');
    await store.send('local', 'a.bgcode', 'usb/a.bgcode');
    let go = (): void => undefined;
    const turn = new Promise<void>((resolve) => {
      go = resolve;
    });

    const replaced = store.replaceTaken(async (work) => {
      await turn;

      return work();
    });
    expect(store.taken).toBeUndefined();
    expect(bodies).toHaveLength(1);

    go();
    await replaced;
    expect(bodies.at(-1)).toEqual({ source: 'a.bgcode', replace: true });
  });

  it('sends a file kept beside the printer’s in its turn too', async () => {
    const bodies: {
      replace?: boolean | undefined;
      keep?: boolean | undefined;
    }[] = [];
    const { client } = stubClient([]);
    client.sendDeviceFile = (request) => {
      bodies.push({
        replace: request.body.replace,
        keep: request.body.keep_both,
      });
      if (request.body.keep_both !== true) {
        throw new StubApiError('core.printer/FILE_NAME_TAKEN', 409);
      }

      return Promise.resolve({
        path: 'usb/a[1].bgcode',
        transfer: transferOf('t-new', 'queued', 0),
      });
    };
    const store = createTransfers(client, 'printer-1');
    await store.send('local', 'a.bgcode', 'usb/a.bgcode');
    let go = (): void => undefined;
    const turn = new Promise<void>((resolve) => {
      go = resolve;
    });

    const kept = store.bothTaken(async (work) => {
      await turn;

      return work();
    });
    expect(store.taken).toBeUndefined();
    expect(bodies).toHaveLength(1);

    go();
    await kept;
    expect(bodies.at(-1)?.keep).toBe(true);
  });

  it('prints a file it sent and reports no fault', async () => {
    const { client, calls } = stubClient([]);
    const store = createTransfers(client, 'printer-1');

    const answered = await store.print(
      'local',
      'cube.bgcode',
      'usb/cube.bgcode',
    );

    expect(calls.sent).toEqual(['cube.bgcode']);
    expect(answered?.answer?.ok).toBe(true);
    expect(answered?.fault).toBeUndefined();
    expect(store.error).toBeUndefined();
  });

  it('reports a start the printer refused after the file landed', async () => {
    const { client } = stubClient([]);
    client.printDeviceFile = (request) =>
      Promise.resolve({
        path: request.body.path,
        command: {
          command_id: 'c-1',
          ok: false,
          fault_code: 'core.printer/COMMAND_REFUSED',
          machine_reason: 'the printer is busy',
        },
      });
    const store = createTransfers(client, 'printer-1');

    const answered = await store.print(
      'local',
      'cube.bgcode',
      'usb/cube.bgcode',
    );

    expect(answered?.answer?.ok).toBe(false);
    expect(answered?.fault?.code).toBe('core.printer/COMMAND_REFUSED');
    expect(store.error?.code).toBe('core.printer/COMMAND_REFUSED');
    expect(store.error?.message).toBe('the printer is busy');
  });

  it('prints the file the printer holds without sending one', async () => {
    const bodies: unknown[] = [];
    const { client } = stubClient([]);
    client.printDeviceFile = (request) => {
      bodies.push(request.body);
      if (bodies.length === 1) {
        throw new StubApiError('core.printer/FILE_NAME_TAKEN', 409);
      }

      return Promise.resolve({
        path: request.body.path,
        command: {
          command_id: 'c-1',
          ok: false,
          machine_reason: 'the printer is busy',
        },
      });
    };
    const store = createTransfers(client, 'printer-1');

    await store.print('local', 'a.bgcode', 'usb/a.bgcode');
    const printed = await store.printTaken();

    // The second request names the printer's file alone, so nothing is sent.
    expect(bodies[1]).toEqual({ path: 'usb/a.bgcode' });
    expect(printed?.answer?.ok).toBe(false);
    expect(printed?.fault?.message).toBe('the printer is busy');
    expect(store.error?.message).toBe('the printer is busy');
    expect(store.taken).toBeUndefined();
  });

  it('answers the fault of a printer file print that failed', async () => {
    const { client } = stubClient([]);
    let calls = 0;
    client.printDeviceFile = () => {
      calls += 1;
      if (calls === 1)
        throw new StubApiError('core.printer/FILE_NAME_TAKEN', 409);

      throw new StubApiError('core.printer/PRINTER_OFFLINE', 409);
    };
    const store = createTransfers(client, 'printer-1');

    await store.print('local', 'a.bgcode', 'usb/a.bgcode');
    const printed = await store.printTaken();

    expect(printed?.answer).toBeUndefined();
    expect(printed?.fault?.code).toBe('core.printer/PRINTER_OFFLINE');
    expect(store.taken).toBeUndefined();
  });

  it('asks about a name the printer holds and prints when it is replaced', async () => {
    const replaced: (boolean | undefined)[] = [];
    let sent = 0;
    const { client } = stubClient([]);
    client.sendDeviceFile = () => {
      sent += 1;

      return Promise.reject(new Error('a print never sends alone'));
    };
    client.printDeviceFile = (request) => {
      replaced.push(request.body.replace);
      if (request.body.replace !== true) {
        throw new StubApiError('core.printer/FILE_NAME_TAKEN', 409);
      }

      return Promise.resolve({
        path: request.body.path,
        command: { command_id: 'c-1', ok: true },
      });
    };
    const store = createTransfers(client, 'printer-1');

    await store.print('local', 'a.bgcode', 'usb/a.bgcode');

    expect(store.error).toBeUndefined();
    expect(store.taken).toEqual({
      storage: 'local',
      source: 'a.bgcode',
      path: 'usb/a.bgcode',
      print: true,
      readOnly: false,
    });

    await store.replaceTaken();

    expect(replaced).toEqual([undefined, true]);
    expect(sent).toBe(0);
    expect(store.taken).toBeUndefined();
  });

  it('stops a transfer and reads the list again', async () => {
    const { client, calls } = stubClient([transferOf('t-1')]);
    const store = createTransfers(client, 'printer-1');
    await store.load();

    await store.stop('t-1');

    expect(calls.stopped).toEqual(['t-1']);
    expect(store.rows[0]?.state).toBe('cancelled');
  });

  it('applies what the stream sends and closes it on teardown', async () => {
    const stream = fakeStream();
    const { client } = stubClient([], fakeEvents(stream));
    const store = createTransfers(client, 'printer-1');
    const stop = store.listen();

    await stream.push(started('t-7'));
    await stream.push(progressed('t-7', 1024));

    expect(store.progress('t-7')).toBe(1);
    stop();
    expect(stream.closed).toBe(true);
  });
});

// A browser can sign out and sign in as somebody else without the page
// reloading. The store then holds nothing of the first person.
describe('Transfers reset', () => {
  it('forgets the transfers of the last person', async () => {
    const { client } = stubClient([transferOf('t-1')]);
    const store = createTransfers(client, 'printer-1');
    await store.load();

    store.reset();

    expect(store.rows).toEqual([]);
    expect(store.inFlight).toEqual([]);
    expect(store.loaded).toBe(false);
    expect(store.loading).toBe(false);
    expect(store.error).toBeUndefined();
  });

  it('drops an answer that was asked for before the reset', async () => {
    let answer: (() => void) | undefined;
    const client: TransfersClient = {
      ...stubClient([]).client,
      listDeviceTransfers: () =>
        new Promise((resolve) => {
          answer = () => {
            resolve({ items: [transferOf('t-1')] });
          };
        }),
    };
    const store = createTransfers(client, 'printer-1');
    const read = store.load();

    store.reset();
    answer?.();
    await read;

    expect(store.rows).toEqual([]);
    expect(store.loaded).toBe(false);
  });

  it('closes the stream, and a new listener opens a new one', () => {
    const streams = [fakeStream(), fakeStream()];
    let opened = 0;
    const events = (() => {
      const next = streams[opened];
      opened += 1;

      return next?.subscription;
    }) as unknown as TransfersClient['events'];
    const { client } = stubClient([], events);
    const store = createTransfers(client, 'printer-1');
    const close = store.listen();

    store.reset();
    expect(streams[0]?.closed).toBe(true);

    // The page that opened the first stream closes it after the reset, and
    // that must not close the stream the next page opened.
    const next = store.listen();
    close();
    expect(opened).toBe(2);
    expect(streams[1]?.closed).toBe(false);
    next();
    expect(streams[1]?.closed).toBe(true);
  });

  // A change of person resets the store first, so the rows a failed read
  // keeps are the same person's.
  it('keeps the rows of the same person beside a failed read', async () => {
    let refused = false;
    const base = stubClient([transferOf('t-1')]).client;
    const client: TransfersClient = {
      ...base,
      listDeviceTransfers: (...args) =>
        refused
          ? Promise.reject(new StubApiError('core.authz/REFUSED', 403))
          : base.listDeviceTransfers(...args),
    };
    const store = createTransfers(client, 'printer-1');
    await store.load();

    refused = true;
    await store.load();

    expect(store.error?.code).toBe('core.authz/REFUSED');
    expect(store.rows.map((row) => row.transferId)).toEqual(['t-1']);
  });
});

describe('Transfers for one person', () => {
  // The SDK reconnects after a drop only while the cookie still belongs to
  // the person named here.
  it('names the person it reads for when it opens the stream', () => {
    const stream = fakeStream();
    const asked: unknown[] = [];
    const events = ((options: unknown) => {
      asked.push(options);

      return stream.subscription;
    }) as unknown as TransfersClient['events'];
    const { client } = stubClient([], events);
    const store = createTransfers(client, 'printer-1', {
      principal: 'user:ada',
    });

    const stop = store.listen();
    stop();

    expect(asked).toEqual([expect.objectContaining({ principal: 'user:ada' })]);
  });
});
