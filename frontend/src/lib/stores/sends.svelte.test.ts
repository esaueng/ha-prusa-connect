import type { DataFault, TransferMoved } from '@prusa/connect-ui/data';
import { flushSync } from 'svelte';
import { describe, expect, it, vi } from 'vitest';

import {
  pendingSendsOf,
  printFirst,
  Sends,
  type SendsRunning,
  type SendsStorage,
  type SendsTransfers,
} from './sends.svelte';

interface Upload {
  storage: string;
  path: string;
  total: number;
  sent: number;
  state: 'sending' | 'done' | 'failed' | 'cancelled';
  fault: undefined | { code: string };
}

// The fakes hold their rows in `$state`, as the real stores do, so the rows
// the store derives follow what a test writes into them.
const fakes = () => {
  const uploads: Upload[] = $state([]);
  let finishUpload:
    ((ok: boolean, state?: Upload['state']) => void) | undefined;
  // Every upload still going up, by its path, to end one out of turn.
  const finishUploads = new Map<
    string,
    (ok: boolean, state?: Upload['state']) => void
  >();
  // Like the Storage store, a second upload of the same key aborts the first,
  // which answers undefined and records nothing.
  const pending = new Map<string, () => void>();
  // The temporary copies asked for, and how to answer each one.
  const temporaries: { storage: string; name: string }[] = [];
  const finishTemporaries: ((ok: boolean) => void)[] = [];
  let reportTemporary: ((sent: number) => void) | undefined;
  const storage: SendsStorage = {
    get uploads() {
      return uploads as never;
    },
    upload: (storage, path, body) =>
      new Promise((resolve) => {
        const key = `${storage}/${path}`;
        pending.get(key)?.();
        pending.set(key, () => {
          resolve(undefined);
        });
        const row: Upload = {
          storage,
          path,
          total: body.size,
          sent: 0,
          state: 'sending',
          fault: undefined,
        };
        uploads.push(row);
        const live = uploads[uploads.length - 1]!;
        const finish = (
          ok: boolean,
          state: Upload['state'] = ok ? 'done' : 'failed',
        ) => {
          pending.delete(key);
          live.state = state;
          live.fault =
            state === 'failed'
              ? { code: 'core.http/BODY_TOO_LARGE' }
              : undefined;
          resolve(ok ? { bytes: body.size, path } : undefined);
        };
        finishUpload = finish;
        finishUploads.set(path, finish);
      }),
    // Cancels the upload of that path, as the Storage store does, and the
    // latest one when the path is not known.
    cancelUpload: (_storage, path) => {
      const finish = finishUploads.get(path) ?? finishUpload;
      finish?.(false, 'cancelled');
    },
    uploadTemporary: (storage, name, body, onProgress, signal) =>
      new Promise((resolve) => {
        temporaries.push({ storage, name });
        const copy = `.pcl-incoming/${String(temporaries.length)}-copy/${name}`;
        signal?.addEventListener('abort', () => {
          resolve({ fault: { code: 'aborted' } as never });
        });
        reportTemporary = (sent) => onProgress?.(sent, body.size);
        finishTemporaries.push((ok) => {
          resolve(
            ok
              ? { saved: { bytes: body.size, path: copy, temporary: true } }
              : { fault: { code: 'core.storage/WRITE_REFUSED' } as never },
          );
        });
      }),
  };

  // Every transfer the server is running to a printer the person may read,
  // as the store of running transfers holds them. A settled transfer leaves.
  interface Running {
    transferId: string;
    deviceId: string;
    printerName: string;
    storage: string;
    source: string;
    path: string;
    bytesTotal: number;
    bytesSent: number;
    state: string;
    mine: boolean;
    startedBy: string | undefined;
    canStop: boolean;
  }
  const rows: Running[] = $state([]);
  // The files a settled frame said landed whole, by what they moved.
  const landed: string[] = $state([]);
  const keyOf = (moved: TransferMoved): string =>
    [moved.deviceId, moved.storage, moved.source, moved.path].join('|');
  const stopped: string[] = [];
  // Who waits for a file to be on its way, by the stored file's path.
  const itsWay: { source: string; run: (path: string) => void }[] = [];
  const stopping: string[] = $state([]);
  // Transfers the server ended whose settled frame never came, and how often
  // the running transfers were read again.
  const ended = new Set<string>();
  let reads = 0;
  const running: SendsRunning = {
    get rows() {
      return rows;
    },
    progress: (id) => {
      const row = rows.find((one) => one.transferId === id);
      return row === undefined || row.bytesTotal <= 0 ? null : 0.5;
    },
    landedAt: (moved) => landed.includes(keyOf(moved)),
    awaitLanding: (moved) => {
      const at = landed.indexOf(keyOf(moved));
      if (at >= 0) landed.splice(at, 1);
    },
    forget: (moved) => {
      const at = landed.indexOf(keyOf(moved));
      if (at >= 0) landed.splice(at, 1);
    },
    whenOnItsWay: (sent, run) => {
      itsWay.push({ source: sent.source, run });
    },
    notOnItsWay: (sent) => {
      const at = itsWay.findIndex((one) => one.source === sent.source);
      if (at >= 0) itsWay.splice(at, 1);
    },
    stop: (id) => {
      stopped.push(id);
      return Promise.resolve();
    },
    stopping: (id) => stopping.includes(id),
    load: () => {
      reads += 1;
      for (let at = rows.length - 1; at >= 0; at -= 1) {
        if (ended.has(rows[at]!.transferId)) rows.splice(at, 1);
      }

      return Promise.resolve();
    },
  };

  const finishSends: ((ok: boolean) => void)[] = [];
  // Whether the printer refuses the start of the next print it takes.
  let refuse = false;
  const sent: string[] = [];
  const printed: string[] = [];
  const sources: string[] = [];
  let error: DataFault | undefined;
  const waiting: { storage: string; source: string; path: string }[] = $state(
    [],
  );
  const relay =
    (deviceId: string, kind: 'send' | 'print') =>
    (storage: string, source: string, path: string): Promise<never> => {
      if (kind === 'print') printed.push(path);
      sent.push(path);
      sources.push(source);
      // The server opens the transfer, and its started frame reaches the
      // store of running transfers while the send waits for the answer.
      const transferId = `t${String(sent.length)}`;
      rows.unshift({
        transferId,
        deviceId,
        printerName: `Printer of ${deviceId}`,
        storage,
        source,
        path,
        bytesTotal: 7,
        bytesSent: 0,
        state: 'sending',
        mine: true,
        startedBy: undefined,
        canStop: true,
      });
      return new Promise((resolve) => {
        // The settled frame drops the row before the answer arrives.
        finishSends.push((ok) => {
          const at = rows.findIndex((one) => one.transferId === transferId);
          if (at >= 0) rows.splice(at, 1);
          // A print answers how it ended, a send the transfer it made.
          const answered =
            kind === 'print'
              ? {
                  answer: { ok: !refuse },
                  fault: refuse
                    ? fault('core.printer/COMMAND_REFUSED')
                    : undefined,
                }
              : { transfer: {} };
          resolve((ok ? answered : undefined) as never);
        });
      });
    };
  const transfersOf = (deviceId: string): SendsTransfers => ({
    get waiting() {
      return waiting;
    },
    get error() {
      return error;
    },
    send: relay(deviceId, 'send'),
    print: relay(deviceId, 'print'),
  });

  return {
    storage,
    transfersOf,
    running,
    uploads,
    rows,
    stopped,
    stopping,
    sent,
    printed,
    sources,
    waiting,
    itsWay,
    finishUpload: (ok: boolean) => finishUpload?.(ok),
    finishUploadOf: (path: string, ok: boolean) =>
      finishUploads.get(path)?.(ok),
    temporaries,
    // Answers every temporary copy still going up.
    finishTemporary: (ok: boolean) => {
      for (const finish of finishTemporaries.splice(0)) finish(ok);
    },
    reportTemporary: (sent: number) => reportTemporary?.(sent),
    // A transfer of the same file to the same place that ended done earlier.
    landEarlier: (moved: TransferMoved) => {
      landed.push(keyOf(moved));
    },
    // Ends a transfer on the server and loses its settled frame, so only a
    // fresh read of the running transfers sees it gone.
    loseSettled: (transferId: string) => {
      ended.add(transferId);
    },
    get reads() {
      return reads;
    },
    // Ends a transfer the server was still running, as its settled frame does.
    endTransfer: (transferId: string, state: string) => {
      const at = rows.findIndex((one) => one.transferId === transferId);
      const ended = rows[at];
      if (ended === undefined) return;
      if (state === 'done') landed.push(keyOf(ended));
      rows.splice(at, 1);
    },
    refuseStart: () => {
      refuse = true;
    },
    // Answers every send still waiting, with `fault` when one is refused.
    finishSend: (ok: boolean, fault?: DataFault) => {
      error = ok ? undefined : fault;
      for (const finish of finishSends.splice(0)) finish(ok);
    },
    // Answers the oldest send still waiting, and no other.
    finishOldestSend: (ok: boolean) => {
      error = undefined;
      finishSends.shift()?.(ok);
    },
  };
};

const file = new File(['1234567'], 'cube.bgcode', { lastModified: 1 });
const fault = (code: string, params: DataFault['params'] = {}): DataFault => ({
  code,
  status: 409,
  params,
  message: undefined,
  traceId: undefined,
});

// A transfer the server is running that this tab did not start.
const elsewhere = (
  over: Partial<{
    transferId: string;
    deviceId: string;
    mine: boolean;
    startedBy: string | undefined;
    canStop: boolean;
    bytesTotal: number;
  }> = {},
) => ({
  transferId: 't-other',
  deviceId: 'p9',
  printerName: 'XL',
  storage: 'local',
  source: 'bracket.bgcode',
  path: 'usb/bracket.bgcode',
  bytesTotal: 7,
  bytesSent: 3,
  state: 'sending',
  mine: false,
  startedBy: undefined,
  canStop: false,
  ...over,
});

const tick = (): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, 0));

const sendsOf = (f: ReturnType<typeof fakes>): Sends =>
  new Sends(f.storage, f.transfersOf, f.running);

describe('Sends', () => {
  it('uploads, then sends to the printer, and leaves rows once it is done', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    expect(sends.rows[0]?.step).toBe('uploading');
    expect(sends.rows[0]?.mine).toBe(true);
    expect(sends.rows[0]?.canCancel).toBe(true);
    expect(sends.uploading).toBe(true);

    f.uploads[0]!.sent = 3;
    expect(sends.rows[0]?.progress).toBeCloseTo(3 / 7);

    f.finishUpload(true);
    await tick();
    expect(sends.rows).toHaveLength(1);
    expect(sends.rows[0]?.step).toBe('sending');
    expect(sends.rows[0]?.progress).toBe(0.5);
    expect(sends.uploading).toBe(false);
    expect(sends.active).toBe(true);
    expect(f.sent).toEqual(['usb/cube.bgcode']);

    f.finishSend(true);
    expect(await done).toBe('done');
    expect(sends.rows).toEqual([]);
    expect(sends.active).toBe(false);
  });

  it('says where the file lands once it is on its way, not when it has landed', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const paths: string[] = [];

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
      onItsWay: (path) => {
        paths.push(path);
      },
    });
    f.finishUpload(true);
    await tick();

    // The store of running transfers runs it from the stream, with the name
    // the server chose when the person kept both files.
    expect(f.itsWay.map((one) => one.source)).toEqual(['cube.bgcode']);
    f.itsWay[0]?.run('usb/cube[1].bgcode');
    expect(paths).toEqual(['usb/cube[1].bgcode']);

    f.finishSend(true);
    await done;
  });

  it('prints a kept file once it is uploaded, and only sending never prints', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const printing = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
      print: true,
    });
    f.finishUpload(true);
    await tick();
    expect(sends.rows[0]?.step).toBe('sending');
    expect(f.printed).toEqual(['usb/cube.bgcode']);

    f.finishSend(true);
    expect(await printing).toBe('done');

    const sending = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    f.finishUpload(true);
    await tick();
    f.finishSend(true);
    expect(await sending).toBe('done');
    expect(f.printed).toEqual(['usb/cube.bgcode']);
    expect(f.sent).toEqual(['usb/cube.bgcode', 'usb/cube.bgcode']);
  });

  it('reads a file the printer took as all there while the send answers', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    f.finishUpload(true);
    await tick();
    f.endTransfer('t1', 'done');
    flushSync();

    expect(sends.rows[0]?.progress).toBe(1);
    f.finishSend(true);
    expect(await done).toBe('done');
  });

  it('does not read an earlier send of the same file as this one landing', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    f.landEarlier({
      deviceId: 'p1',
      storage: 'local',
      source: 'cube.bgcode',
      path: 'usb/cube.bgcode',
    });

    void sends.send(file, 'p1', 'Printer 1', 'local', 'usb', { keep: '' });
    f.finishUpload(true);
    await tick();
    f.endTransfer('t1', 'failed');
    flushSync();

    expect(sends.rows[0]?.step).toBe('checking');
    expect(sends.rows[0]?.progress).toBeNull();
  });

  it('answers refused for a print the printer would not start', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    f.refuseStart();
    const refused: unknown[] = [];

    const printing = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
      print: true,
      onRefused: (ended) => refused.push(ended.fault?.code),
    });
    f.finishUpload(true);
    await tick();
    f.finishSend(true);

    expect(await printing).toBe('refused');
    expect(refused).toEqual(['core.printer/COMMAND_REFUSED']);
  });

  it('prints a temporary copy once it is uploaded', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const printing = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      print: true,
    });
    f.finishTemporary(true);
    await tick();
    expect(f.printed).toEqual(['usb/cube.bgcode']);

    f.finishSend(true);
    expect(await printing).toBe('done');
  });

  it('never sends a file whose upload was refused, with the Storage row failed', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    f.finishUpload(false);

    expect(await done).toBe('failed');
    expect(sends.rows).toEqual([]);
    expect(f.sent).toEqual([]);
  });

  it('a cancelled upload is cancelled, not failed, and sends nothing', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    const id = sends.rows[0]!.id;
    sends.cancel(id);

    expect(await done).toBe('cancelled');
    expect(sends.rows).toEqual([]);
    expect(f.sent).toEqual([]);
  });

  it('joined uploads: both sends resolve done, and both rows go', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const first = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    const same = new File(['1234567'], 'cube.bgcode', { lastModified: 1 });
    const second = sends.send(same, 'p2', 'Printer 2', 'local', 'sd', {
      keep: '',
    });
    expect(f.uploads).toHaveLength(1);
    expect(sends.rows.map((row) => row.printerName)).toEqual([
      'Printer 2',
      'Printer 1',
    ]);

    f.uploads[0]!.sent = 3;
    expect(sends.rows.map((row) => row.progress)).toEqual([3 / 7, 3 / 7]);

    f.finishUpload(true);
    await tick();
    expect(f.sent).toEqual(['usb/cube.bgcode', 'sd/cube.bgcode']);
    // Each job claims the transfer to its own printer, so nothing is drawn
    // twice.
    expect(sends.rows.map((row) => row.id)).toEqual(['job:2', 'job:1']);

    f.finishSend(true);
    expect(await Promise.all([first, second])).toEqual(['done', 'done']);
    expect(sends.rows).toEqual([]);
  });

  it('a different file of the same name replaces the upload, and the first ends cancelled', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const first = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    const other = new File(['123'], 'cube.bgcode', { lastModified: 1 });
    const second = sends.send(other, 'p2', 'Printer 2', 'local', 'sd', {
      keep: '',
    });
    expect(f.uploads).toHaveLength(2);
    expect(await first).toBe('cancelled');
    expect(sends.rows).toHaveLength(1);

    f.finishUpload(true);
    await tick();
    expect(f.sent).toEqual(['sd/cube.bgcode']);

    f.finishSend(true);
    expect(await second).toBe('done');
    expect(sends.rows).toEqual([]);
  });

  it('cancelling one of two sends that share an upload cancels both', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const first = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    const second = sends.send(file, 'p2', 'Printer 2', 'local', 'sd', {
      keep: '',
    });
    sends.cancel(sends.rows[0]!.id);

    expect(await Promise.all([first, second])).toEqual([
      'cancelled',
      'cancelled',
    ]);
    expect(sends.rows).toEqual([]);
    expect(f.sent).toEqual([]);
  });

  it('uploads a file again once its earlier upload has ended', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const first = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    f.finishUpload(false);
    await first;
    void sends.send(file, 'p2', 'Printer 2', 'local', 'sd', { keep: '' });

    expect(f.uploads).toHaveLength(2);
  });

  it('a transfer stopped on the printer page ends cancelled, not failed', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    f.finishUpload(true);
    await tick();
    f.finishSend(
      false,
      fault('connector/TRANSFER_FAILED', { event: 'stopped' }),
    );

    expect(await done).toBe('cancelled');
    expect(sends.rows).toEqual([]);
  });

  it('sends files dropped together to one printer one after another, and every one lands', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) => new File(['1234567'], name);

    const outcomes = ['a.bgcode', 'b.bgcode', 'c.bgcode'].map((name) =>
      sends.send(named(name), 'p1', 'Printer 1', 'local', 'usb'),
    );
    // A temporary copy goes up only once its turn has come: the server
    // deletes one nobody has sent for an hour, and a long line outlasts that.
    expect(f.temporaries.map((one) => one.name)).toEqual(['a.bgcode']);
    expect(sends.rows.map((row) => row.step)).toEqual([
      'waiting',
      'waiting',
      'uploading',
    ]);
    expect(sends.rows[0]?.progress).toBeNull();
    expect(sends.rows[0]?.canCancel).toBe(true);
    // Closing the tab would lose the files still waiting.
    expect(sends.uploading).toBe(true);

    f.finishTemporary(true);
    await tick();
    // The printer fetches one file at a time, so the others wait their turn.
    expect(f.sent).toEqual(['usb/a.bgcode']);

    f.finishOldestSend(true);
    await tick();
    expect(f.temporaries.map((one) => one.name)).toEqual([
      'a.bgcode',
      'b.bgcode',
    ]);
    f.finishTemporary(true);
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode']);

    f.finishOldestSend(true);
    await tick();
    f.finishTemporary(true);
    await tick();
    f.finishOldestSend(true);

    expect(await Promise.all(outcomes)).toEqual(['done', 'done', 'done']);
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode', 'usb/c.bgcode']);
    expect(sends.rows).toEqual([]);
  });

  it('a kept file goes up at once and waits on this server for its turn', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const first = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    f.finishUpload(true);
    await tick();
    const second = sends.send(
      new File(['1234567'], 'b.bgcode', { lastModified: 1 }),
      'p1',
      'Printer 1',
      'local',
      'usb',
      { keep: '' },
    );
    expect(sends.rows[0]?.step).toBe('uploading');
    f.finishUpload(true);
    await tick();
    expect(sends.rows[0]?.step).toBe('waiting');
    expect(f.sent).toEqual(['usb/cube.bgcode']);

    f.finishOldestSend(true);
    await tick();
    expect(f.sent).toEqual(['usb/cube.bgcode', 'usb/b.bgcode']);
    f.finishOldestSend(true);
    expect(await first).toBe('done');
    expect(await second).toBe('done');
  });

  it('sends kept files in the order they were asked for, not in the order their uploads end', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) =>
      new File(['1234567'], name, { lastModified: 1 });

    const first = sends.send(
      named('a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      {
        keep: '',
        print: true,
      },
    );
    const second = sends.send(
      named('b.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      {
        keep: '',
      },
    );
    f.finishUploadOf('b.bgcode', true);
    await tick();
    expect(f.sent).toEqual([]);
    expect(sends.rows.map((row) => row.step)).toEqual(['waiting', 'uploading']);

    f.finishUploadOf('a.bgcode', true);
    await tick();
    expect(f.printed).toEqual(['usb/a.bgcode']);
    f.finishOldestSend(true);
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode']);
    f.finishOldestSend(true);
    expect(await first).toBe('done');
    expect(await second).toBe('done');
  });

  it('a kept file whose upload fails while the one before is still sending hands its turn on', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) =>
      new File(['1234567'], name, { lastModified: 1 });
    const keep = { keep: '' };

    const first = sends.send(
      named('a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      keep,
    );
    const second = sends.send(
      named('b.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      keep,
    );
    const third = sends.send(
      named('c.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      keep,
    );
    f.finishUploadOf('a.bgcode', true);
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode']);

    f.finishUploadOf('b.bgcode', false);
    expect(await second).toBe('failed');
    f.finishUploadOf('c.bgcode', true);
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode']);

    f.finishOldestSend(true);
    expect(await first).toBe('done');
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/c.bgcode']);
    f.finishOldestSend(true);
    expect(await third).toBe('done');
  });

  it('a kept file cancelled while it waits for the printer hands its turn on', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) =>
      new File(['1234567'], name, { lastModified: 1 });
    const keep = { keep: '' };

    const first = sends.send(
      named('a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      keep,
    );
    const second = sends.send(
      named('b.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      keep,
    );
    const third = sends.send(
      named('c.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      keep,
    );
    for (const name of ['a.bgcode', 'b.bgcode', 'c.bgcode']) {
      f.finishUploadOf(name, true);
    }
    await tick();
    const waiting = sends.rows.find((row) => row.name === 'b.bgcode');
    expect(waiting?.step).toBe('waiting');

    sends.cancel(waiting!.id);
    expect(await second).toBe('cancelled');
    f.finishOldestSend(true);
    expect(await first).toBe('done');
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/c.bgcode']);
    f.finishOldestSend(true);
    expect(await third).toBe('done');
  });

  it('a kept file cancelled while it still goes up hands its turn on', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) =>
      new File(['1234567'], name, { lastModified: 1 });
    const keep = { keep: '' };

    const first = sends.send(
      named('a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      keep,
    );
    const second = sends.send(
      named('b.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      keep,
    );
    const third = sends.send(
      named('c.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      keep,
    );
    f.finishUploadOf('a.bgcode', true);
    f.finishUploadOf('c.bgcode', true);
    await tick();

    const uploading = sends.rows.find((row) => row.name === 'b.bgcode');
    expect(uploading?.step).toBe('uploading');
    sends.cancel(uploading!.id);
    expect(await second).toBe('cancelled');
    f.finishOldestSend(true);
    expect(await first).toBe('done');
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/c.bgcode']);
    f.finishOldestSend(true);
    expect(await third).toBe('done');
  });

  it('a kept file whose upload fails hands its turn on', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) =>
      new File(['1234567'], name, { lastModified: 1 });

    const first = sends.send(
      named('a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      {
        keep: '',
      },
    );
    const second = sends.send(
      named('b.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      {
        keep: '',
      },
    );
    f.finishUploadOf('a.bgcode', false);
    expect(await first).toBe('failed');
    f.finishUploadOf('b.bgcode', true);
    await tick();
    expect(f.sent).toEqual(['usb/b.bgcode']);
    f.finishOldestSend(true);
    expect(await second).toBe('done');
  });

  it('a file waiting for the printer can be cancelled, and the next one goes in its place', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) => new File(['1234567'], name);

    const first = sends.send(
      named('a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    const second = sends.send(
      named('b.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    const third = sends.send(
      named('c.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    f.finishTemporary(true);
    await tick();

    sends.cancel(sends.rows[1]!.id);
    expect(await second).toBe('cancelled');
    expect(sends.rows.map((row) => row.name)).toEqual(['c.bgcode', 'a.bgcode']);

    f.finishOldestSend(true);
    await tick();
    f.finishTemporary(true);
    await tick();
    expect(f.temporaries.map((one) => one.name)).toEqual([
      'a.bgcode',
      'c.bgcode',
    ]);
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/c.bgcode']);

    f.finishOldestSend(true);
    expect(await first).toBe('done');
    expect(await third).toBe('done');
  });

  it('a reset ends the files still waiting, and none of them is sent', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) => new File(['1234567'], name);

    const first = sends.send(
      named('a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    const waiting = ['b.bgcode', 'c.bgcode'].map((name) =>
      sends.send(named(name), 'p1', 'Printer 1', 'local', 'usb'),
    );
    f.finishTemporary(true);
    await tick();

    sends.reset();
    expect(await Promise.all(waiting)).toEqual(['cancelled', 'cancelled']);
    f.finishOldestSend(true);
    expect(await first).toBe('cancelled');
    await tick();

    expect(f.temporaries.map((one) => one.name)).toEqual(['a.bgcode']);
    expect(f.sent).toEqual(['usb/a.bgcode']);
    expect(sends.rows).toEqual([]);
  });

  it('files for two printers do not wait for each other', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) => new File(['1234567'], name);

    void sends.send(named('a.bgcode'), 'p1', 'Printer 1', 'local', 'usb');
    void sends.send(named('b.bgcode'), 'p2', 'Printer 2', 'local', 'usb');
    f.finishTemporary(true);
    await tick();

    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode']);
  });

  it('the next file goes once the one before failed', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) => new File(['1234567'], name);

    const first = sends.send(
      named('a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    const second = sends.send(
      named('b.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    f.finishTemporary(true);
    await tick();

    f.finishOldestSend(false);
    expect(await first).toBe('failed');
    await tick();
    f.finishTemporary(true);
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode']);

    f.finishOldestSend(true);
    expect(await second).toBe('done');
  });

  it('a refused send ends failed', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    f.finishUpload(true);
    await tick();
    // Refused before any transfer opened: no transfer is running.
    f.rows.shift();
    f.finishSend(false, fault('connector/TRANSFER_BUSY'));

    expect(await done).toBe('failed');
    expect(sends.rows).toEqual([]);
  });

  it('a send refused before any transfer takes back who waited for it', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
      onItsWay: () => undefined,
    });
    f.finishUpload(true);
    await tick();
    expect(f.itsWay).toHaveLength(1);
    f.rows.shift();
    f.finishSend(false, fault('connector/TRANSFER_BUSY'));

    expect(await done).toBe('failed');
    expect(f.itsWay).toEqual([]);
  });

  it('a send that asks about a taken name keeps who waits for it', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
      onItsWay: () => undefined,
    });
    f.finishUpload(true);
    await tick();
    f.rows.shift();
    f.waiting.push({
      storage: 'local',
      source: 'cube.bgcode',
      path: 'usb/cube.bgcode',
    });
    f.finishSend(false, fault('core.printer/FILE_NAME_TAKEN'));

    expect(await done).toBe('taken');
    expect(f.itsWay).toHaveLength(1);
  });

  it('reports no progress while the printer has not said how long the file is', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    void sends.send(file, 'p1', 'Printer 1', 'local', 'usb', { keep: '' });
    f.finishUpload(true);
    await tick();
    f.rows[0]!.bytesTotal = 0;

    expect(sends.rows[0]?.step).toBe('sending');
    expect(sends.rows[0]?.progress).toBeNull();
  });

  it('reports no progress and offers no stop before the server says the transfer started', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    void sends.send(file, 'p1', 'Printer 1', 'local', 'usb', { keep: '' });
    f.finishUpload(true);
    await tick();
    f.rows.shift();

    expect(sends.rows[0]?.step).toBe('checking');
    expect(sends.rows[0]?.progress).toBeNull();
    expect(sends.rows[0]?.canCancel).toBe(false);
  });

  it('a send the printer did not take ends failed', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    f.finishUpload(true);
    await tick();
    f.finishSend(false);

    expect(await done).toBe('failed');
    expect(sends.rows).toEqual([]);
  });

  it('a reset answers cancelled, and a chain from before records nothing', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    sends.reset();
    f.finishUpload(true);

    expect(await done).toBe('cancelled');
    expect(sends.rows).toEqual([]);
    expect(f.sent).toEqual([]);
  });

  it('draws every file the server is sending, whoever started it', () => {
    const f = fakes();
    f.rows.push(
      elsewhere({ transferId: 't-ada', startedBy: 'user:ada' }),
      elsewhere({ transferId: 't-mine', deviceId: 'p8', mine: true }),
    );
    const sends = sendsOf(f);

    expect(sends.rows).toEqual([
      {
        id: 'transfer:t-ada',
        deviceId: 'p9',
        printerName: 'XL',
        name: 'bracket.bgcode',
        path: 'usb/bracket.bgcode',
        step: 'sending',
        progress: 0.5,
        mine: false,
        startedBy: 'user:ada',
        canCancel: false,
        stopping: false,
      },
      expect.objectContaining({ id: 'transfer:t-mine', mine: true }),
    ]);
    expect(sends.active).toBe(true);
    // The browser holds none of these bytes, so closing the tab asks nothing.
    expect(sends.uploading).toBe(false);

    // The file lands, and the row goes with it.
    f.rows.splice(0, 2);
    expect(sends.rows).toEqual([]);
  });

  it('stops a file somebody else started only when the person may', () => {
    const f = fakes();
    f.rows.push(
      elsewhere({ transferId: 't-free', canStop: true }),
      elsewhere({ transferId: 't-held', deviceId: 'p7' }),
    );
    const sends = sendsOf(f);

    sends.cancel('transfer:t-held');
    sends.cancel('transfer:t-free');

    expect(f.stopped).toEqual(['t-free']);
  });

  it('stops its own file on its way to the printer through the transfer the server runs', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    void sends.send(file, 'p1', 'Printer 1', 'local', 'usb', { keep: '' });
    f.finishUpload(true);
    await tick();
    expect(sends.rows[0]?.canCancel).toBe(true);

    sends.cancel(sends.rows[0]!.id);

    expect(f.stopped).toEqual(['t1']);
  });

  it('says a stop is on its way', () => {
    const f = fakes();
    f.rows.push(elsewhere({ canStop: true }));
    f.stopping.push('t-other');
    const sends = sendsOf(f);

    expect(sends.rows[0]?.stopping).toBe(true);
  });

  it('draws a file it is waiting for once, as its own row', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    void sends.send(file, 'p1', 'Printer 1', 'local', 'usb', { keep: '' });
    f.finishUpload(true);
    await tick();

    // The server runs the transfer, and the job waits for it.
    expect(sends.rows.map((row) => row.id)).toEqual(['job:1']);

    // Another file on its way to the same printer, sent from somewhere
    // else, is drawn beside it.
    f.rows.push(
      elsewhere({ transferId: 'other', deviceId: 'p1', bytesTotal: 0 }),
    );
    expect(sends.rows.map((row) => [row.id, row.progress])).toEqual([
      ['job:1', 0.5],
      ['transfer:other', null],
    ]);
  });

  // The server asks the printer about the name before it opens a transfer,
  // which takes a printer seconds.
  it('says the printer is being checked until the transfer is listed', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    void sends.send(file, 'p1', 'Printer 1', 'local', 'usb', { keep: '' });
    f.finishUpload(true);
    await tick();
    const opened = f.rows.shift();
    expect(sends.rows.map((row) => row.step)).toEqual(['checking']);

    if (opened !== undefined) f.rows.push(opened);
    expect(sends.rows.map((row) => row.step)).toEqual(['sending']);
  });

  it('a name the printer holds ends taken', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: '',
    });
    f.finishUpload(true);
    await tick();
    // The printer refused the name, so no transfer opened and the store
    // waits for the person.
    f.rows.shift();
    f.waiting.push({
      storage: 'local',
      source: 'cube.bgcode',
      path: 'usb/cube.bgcode',
    });
    f.finishSend(false);

    expect(await done).toBe('taken');
    // Drawn until the person answers.
    expect(sends.rows.map((row) => row.step)).toEqual(['asking']);
  });

  it('a reset forgets the files this tab is sending', () => {
    const f = fakes();
    const sends = sendsOf(f);
    void sends.send(file, 'p1', 'Printer 1', 'local', 'usb', { keep: '' });
    expect(sends.rows).toHaveLength(1);

    sends.reset();

    expect(sends.rows).toEqual([]);
  });

  it('recomputes the rows once for a write, and not at all for a write that changes nothing', () => {
    const f = fakes();
    const sends = sendsOf(f);
    let runs = 0;
    let seen: readonly unknown[] = [];

    const stop = $effect.root(() => {
      $effect(() => {
        seen = sends.rows;
        runs += 1;
      });
    });
    flushSync();
    expect(runs).toBe(1);

    void sends.send(file, 'p1', 'Printer 1', 'local', 'usb', { keep: '' });
    flushSync();
    expect(runs).toBe(2);

    // A cancel of a row this store does not hold writes nothing.
    sends.cancel('job:42');
    flushSync();
    expect(runs).toBe(2);
    expect(seen).toHaveLength(1);

    stop();
  });
});

describe('Sends, kept or not', () => {
  it('sends a temporary copy by default: its own progress, no Storage row, and the source the server answered', async () => {
    const f = fakes();
    const sends = new Sends(f.storage, f.transfersOf, f.running);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb');
    expect(f.temporaries).toEqual([{ storage: 'local', name: 'cube.bgcode' }]);
    expect(f.uploads).toEqual([]);
    expect(sends.rows[0]?.step).toBe('uploading');
    expect(sends.rows[0]?.progress).toBe(0);

    f.reportTemporary(3);
    expect(sends.rows[0]?.progress).toBeCloseTo(3 / 7);

    f.finishTemporary(true);
    await tick();
    expect(sends.rows[0]?.step).toBe('sending');
    expect(f.sources).toEqual(['.pcl-incoming/1-copy/cube.bgcode']);
    expect(f.sent).toEqual(['usb/cube.bgcode']);

    f.finishSend(true);
    expect(await done).toBe('done');
    expect(sends.rows).toEqual([]);
  });

  it('never shares a temporary copy: the same file for two printers goes up twice', async () => {
    const f = fakes();
    const sends = new Sends(f.storage, f.transfersOf, f.running);

    const first = sends.send(file, 'p1', 'Printer 1', 'local', 'usb');
    const second = sends.send(file, 'p2', 'Printer 2', 'local', 'sd');
    expect(f.temporaries).toHaveLength(2);

    f.finishTemporary(true);
    await tick();
    expect(f.sources).toEqual([
      '.pcl-incoming/1-copy/cube.bgcode',
      '.pcl-incoming/2-copy/cube.bgcode',
    ]);

    f.finishSend(true);
    expect(await first).toBe('done');
    expect(await second).toBe('done');
  });

  it('stops a temporary copy on its way: cancelled, and nothing sent', async () => {
    const f = fakes();
    const sends = new Sends(f.storage, f.transfersOf, f.running);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb');
    sends.cancel(sends.rows[0]!.id);

    expect(await done).toBe('cancelled');
    expect(sends.rows).toEqual([]);
    expect(f.sent).toEqual([]);
  });

  it('a refused temporary copy fails and sends nothing', async () => {
    const f = fakes();
    const sends = new Sends(f.storage, f.transfersOf, f.running);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb');
    f.finishTemporary(false);

    expect(await done).toBe('failed');
    expect(f.sent).toEqual([]);
  });

  it('a temporary copy refused for a name the printer holds is taken, matched by its own source', async () => {
    const f = fakes();
    const sends = new Sends(f.storage, f.transfersOf, f.running);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb');
    f.finishTemporary(true);
    await tick();
    f.waiting.push({
      storage: 'local',
      source: '.pcl-incoming/1-copy/cube.bgcode',
      path: 'usb/cube.bgcode',
    });
    f.finishSend(false, fault('core.printer/FILE_NAME_TAKEN'));

    expect(await done).toBe('taken');
  });

  it('a reset stops a temporary copy on its way', async () => {
    const f = fakes();
    const sends = new Sends(f.storage, f.transfersOf, f.running);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb');
    sends.reset();

    expect(await done).toBe('cancelled');
    expect(f.sent).toEqual([]);
  });

  it('keeps a file in the folder chosen and sends it from there', async () => {
    const f = fakes();
    const sends = new Sends(f.storage, f.transfersOf, f.running);

    const done = sends.send(file, 'p1', 'Printer 1', 'local', 'usb', {
      keep: 'prints/brackets',
    });
    expect(f.temporaries).toEqual([]);
    expect(f.uploads[0]?.path).toBe('prints/brackets/cube.bgcode');

    f.finishUpload(true);
    await tick();
    expect(f.sources).toEqual(['prints/brackets/cube.bgcode']);

    f.finishSend(true);
    expect(await done).toBe('done');
  });
});

describe('Sends and a transfer from elsewhere', () => {
  it('waits for a transfer another tab runs to the same printer', async () => {
    const f = fakes();
    f.rows.push(elsewhere({ transferId: 't-tab', deviceId: 'p1' }));
    const sends = sendsOf(f);

    const sent = sends.send(
      new File(['1234567'], 'a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    expect(sends.rows.find((row) => row.name === 'a.bgcode')?.step).toBe(
      'waiting',
    );
    await tick();
    expect(f.temporaries).toEqual([]);

    f.rows.splice(0, 1);
    flushSync();
    await tick();
    f.finishTemporary(true);
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode']);
    f.finishOldestSend(true);
    expect(await sent).toBe('done');
  });

  it('does not wait for a transfer to another printer', async () => {
    const f = fakes();
    f.rows.push(elsewhere({ transferId: 't-other', deviceId: 'p9' }));
    const sends = sendsOf(f);

    void sends.send(
      new File(['1234567'], 'a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    f.finishTemporary(true);
    await tick();

    expect(f.sent).toEqual(['usb/a.bgcode']);
  });

  it('waits for a transfer somebody started while the file went up', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const sent = sends.send(
      new File(['1234567'], 'a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    f.rows.push(elsewhere({ transferId: 't-tab', deviceId: 'p1' }));
    f.finishTemporary(true);
    await tick();
    expect(f.sent).toEqual([]);
    expect(sends.rows.find((row) => row.name === 'a.bgcode')?.step).toBe(
      'waiting',
    );

    f.endTransfer('t-tab', 'done');
    flushSync();
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode']);
    f.finishOldestSend(true);
    expect(await sent).toBe('done');
  });

  it('a file waiting for a transfer started during its upload can be cancelled', async () => {
    const f = fakes();
    const sends = sendsOf(f);

    const sent = sends.send(
      new File(['1234567'], 'a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    f.rows.push(elsewhere({ transferId: 't-tab', deviceId: 'p1' }));
    f.finishTemporary(true);
    await tick();
    const row = sends.rows.find((one) => one.name === 'a.bgcode');
    sends.cancel(row!.id);

    expect(await sent).toBe('cancelled');
    expect(f.sent).toEqual([]);
  });

  // A settled frame lost without a continuity event leaves the transfer among
  // the running ones, and the line must not wait for it for good.
  it('reads the running transfers again while a file waits, so a lost settled frame does not hold the line', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    try {
      const f = fakes();
      f.rows.push(elsewhere({ transferId: 't-tab', deviceId: 'p1' }));
      const sends = sendsOf(f);
      const sent = sends.send(
        new File(['1234567'], 'a.bgcode'),
        'p1',
        'Printer 1',
        'local',
        'usb',
      );
      f.loseSettled('t-tab');
      await tick();
      expect(f.temporaries).toEqual([]);

      vi.advanceTimersByTime(29_000);
      expect(f.reads).toBe(0);
      vi.advanceTimersByTime(1_000);
      expect(f.reads).toBe(1);
      flushSync();
      await tick();
      f.finishTemporary(true);
      await tick();
      expect(f.sent).toEqual(['usb/a.bgcode']);

      // Nothing is read again once the printer is free.
      vi.advanceTimersByTime(60_000);
      expect(f.reads).toBe(1);
      f.finishOldestSend(true);
      expect(await sent).toBe('done');
    } finally {
      vi.useRealTimers();
    }
  });

  it('a file waiting for a transfer from elsewhere can be cancelled', async () => {
    const f = fakes();
    f.rows.push(elsewhere({ transferId: 't-tab', deviceId: 'p1' }));
    const sends = sendsOf(f);

    const sent = sends.send(
      new File(['1234567'], 'a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    const row = sends.rows.find((one) => one.name === 'a.bgcode');
    sends.cancel(row!.id);

    expect(await sent).toBe('cancelled');
    expect(f.temporaries).toEqual([]);
  });
});

describe('Sends and a name the printer holds', () => {
  // Drops a print of a.bgcode and a copy of b.bgcode, and lets a.bgcode end
  // on a name the printer holds, as the server answers it.
  const dropWithTakenPrint = async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) => new File(['1234567'], name);
    const printed = sends.send(
      named('a.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
      {
        print: true,
      },
    );
    const copied = sends.send(
      named('b.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    f.finishTemporary(true);
    await tick();
    f.rows.shift();
    f.waiting.push({
      storage: 'local',
      source: f.sources[0]!,
      path: 'usb/a.bgcode',
    });
    f.finishOldestSend(false);
    expect(await printed).toBe('taken');
    await tick();

    return { f, sends, copied };
  };

  it('holds the printer while the person is asked, so no copy goes ahead of the file to print', async () => {
    const { f } = await dropWithTakenPrint();

    expect(f.temporaries.map((one) => one.name)).toEqual(['a.bgcode']);
    expect(f.sent).toEqual(['usb/a.bgcode']);
  });

  it('sends the replacing file in the place it held, before the copies', async () => {
    const { f, sends, copied } = await dropWithTakenPrint();
    const order: string[] = [];

    f.waiting.shift();
    const replaced = sends.inTurn('p1', () => {
      order.push('replace');
      return Promise.resolve();
    });
    await replaced;
    await tick();
    f.finishTemporary(true);
    await tick();
    order.push(...f.sent.slice(1));

    expect(order).toEqual(['replace', 'usb/b.bgcode']);
    f.finishOldestSend(true);
    expect(await copied).toBe('done');
  });

  it('sends the replacing file only once a transfer started elsewhere has ended', async () => {
    const { f, sends } = await dropWithTakenPrint();
    const ran: string[] = [];
    // Somebody else sends to the printer while the person is asked.
    f.rows.push(elsewhere({ transferId: 't-tab', deviceId: 'p1' }));

    f.waiting.shift();
    let finish = (): void => undefined;
    const replaced = sends.inTurn(
      'p1',
      () =>
        new Promise<void>((resolve) => {
          ran.push('replace');
          finish = resolve;
        }),
    );
    await tick();
    expect(ran).toEqual([]);
    const row = () => sends.rows.find((one) => one.name === 'a.bgcode');
    // It waits like any file, and only then reads as deleting the printer's
    // file to make room.
    expect(row()?.step).toBe('waiting');
    expect(row()?.canCancel).toBe(true);

    f.endTransfer('t-tab', 'done');
    flushSync();
    await tick();
    expect(row()?.step).toBe('replacing');
    expect(ran).toEqual(['replace']);
    finish();
    await replaced;
  });

  it('leaves the printer’s file when the person cancels a replace still waiting, and lets the copies go', async () => {
    const { f, sends, copied } = await dropWithTakenPrint();
    const ran: string[] = [];
    f.rows.push(elsewhere({ transferId: 't-tab', deviceId: 'p1' }));

    f.waiting.shift();
    const replaced = sends.inTurn('p1', () => {
      ran.push('replace');
      return Promise.resolve('replaced');
    });
    await tick();
    const row = sends.rows.find((one) => one.name === 'a.bgcode');
    sends.cancel(row!.id);

    expect(await replaced).toBeUndefined();
    expect(ran).toEqual([]);
    expect(sends.rows.find((one) => one.name === 'a.bgcode')).toBeUndefined();

    f.endTransfer('t-tab', 'done');
    flushSync();
    await tick();
    f.finishTemporary(true);
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode']);
    f.finishOldestSend(true);
    expect(await copied).toBe('done');
  });

  it('draws the file while the person is asked, and as replacing once it is replaced', async () => {
    const { f, sends } = await dropWithTakenPrint();
    const stepOf = (name: string) =>
      sends.rows.find((row) => row.name === name)?.step;

    expect(stepOf('a.bgcode')).toBe('asking');
    // It keeps its place among the files of the drop, ahead of the copy.
    expect(pendingSendsOf(sends.rows, 'p1', []).map((row) => row.name)).toEqual(
      ['a.bgcode', 'b.bgcode'],
    );
    expect(sends.rows.find((row) => row.name === 'a.bgcode')?.canCancel).toBe(
      false,
    );

    f.waiting.shift();
    let finish = (): void => undefined;
    const replaced = sends.inTurn(
      'p1',
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    await tick();
    expect(stepOf('a.bgcode')).toBe('replacing');

    finish();
    await replaced;
    expect(stepOf('a.bgcode')).toBeUndefined();
  });

  it('drops the file once the person keeps the one on the printer', async () => {
    const { f, sends } = await dropWithTakenPrint();

    f.waiting.shift();
    flushSync();
    await tick();

    expect(sends.rows.find((row) => row.name === 'a.bgcode')).toBeUndefined();
  });

  // A kept file goes up beside the others and takes its turn afterwards, so
  // its place was waited for and not given at once.
  it('holds the printer for a kept file that waited for its turn', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) => new File(['1234567'], name);
    const drop = (name: string) =>
      sends.send(named(name), 'p1', 'Printer 1', 'local', 'usb', { keep: '' });
    const first = drop('a.bgcode');
    const second = drop('b.bgcode');
    const third = drop('c.bgcode');
    for (const name of ['a.bgcode', 'b.bgcode', 'c.bgcode']) {
      f.finishUploadOf(name, true);
    }
    await tick();
    f.finishOldestSend(true);
    expect(await first).toBe('done');
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode']);

    // The printer holds b.bgcode, and the person is asked.
    f.rows.shift();
    f.waiting.push({
      storage: 'local',
      source: 'b.bgcode',
      path: 'usb/b.bgcode',
    });
    f.finishOldestSend(false);
    expect(await second).toBe('taken');
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode']);

    f.waiting.shift();
    flushSync();
    await tick();
    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode', 'usb/c.bgcode']);
    f.finishOldestSend(true);
    expect(await third).toBe('done');
  });

  it('lets the copies go once the person keeps the file on the printer', async () => {
    const { f, copied } = await dropWithTakenPrint();

    f.waiting.shift();
    flushSync();
    await tick();
    f.finishTemporary(true);
    await tick();

    expect(f.sent).toEqual(['usb/a.bgcode', 'usb/b.bgcode']);
    f.finishOldestSend(true);
    expect(await copied).toBe('done');
  });
});

describe('Sends.inTurn', () => {
  it('runs a replace only once the file going to the printer has landed', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const ran: string[] = [];

    const copy = sends.send(
      new File(['1234567'], 'b.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    f.finishTemporary(true);
    await tick();
    expect(f.sent).toEqual(['usb/b.bgcode']);

    const replaced = sends.inTurn('p1', () => {
      ran.push('replace');
      return Promise.resolve('replaced');
    });
    await tick();
    expect(ran).toEqual([]);

    f.finishOldestSend(true);
    expect(await copy).toBe('done');
    expect(await replaced).toBe('replaced');
    expect(ran).toEqual(['replace']);
  });

  it('runs at once on a free printer, and holds the printer until it ends', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    let finish = (): void => undefined;

    const replaced = sends.inTurn(
      'p1',
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const next = sends.send(
      new File(['1234567'], 'c.bgcode'),
      'p1',
      'Printer 1',
      'local',
      'usb',
    );
    await tick();
    expect(f.temporaries).toEqual([]);

    finish();
    await replaced;
    await tick();
    f.finishTemporary(true);
    await tick();
    expect(f.sent).toEqual(['usb/c.bgcode']);
    f.finishOldestSend(true);
    expect(await next).toBe('done');
  });
});

describe('pendingSendsOf', () => {
  it('draws each file of a drop once, oldest first, from the drop until its transfer is listed', async () => {
    const f = fakes();
    const sends = sendsOf(f);
    const named = (name: string) => new File(['1234567'], name);
    const page = (moving: { path: string; state: string }[] = []) =>
      pendingSendsOf(sends.rows, 'p1', moving).map(
        (row) => `${row.name}:${row.step}`,
      );

    for (const name of ['a.bgcode', 'b.bgcode']) {
      void sends.send(named(name), 'p1', 'Printer 1', 'local', 'usb');
    }
    void sends.send(named('x.bgcode'), 'p2', 'Printer 2', 'local', 'usb');
    expect(page()).toEqual(['a.bgcode:uploading', 'b.bgcode:waiting']);

    f.finishTemporary(true);
    await tick();
    // On its way to the printer, before the server lists the transfer.
    expect(page()).toEqual(['a.bgcode:sending', 'b.bgcode:waiting']);
    // The transfer is listed, so the page draws it there and not here.
    expect(page([{ path: '/usb/a.bgcode', state: 'sending' }])).toEqual([
      'b.bgcode:waiting',
    ]);
    // A transfer of the same name that ended before this send is not its own.
    expect(page([{ path: 'usb/a.bgcode', state: 'done' }])).toEqual([
      'a.bgcode:sending',
      'b.bgcode:waiting',
    ]);

    f.finishOldestSend(true);
    await tick();
    // The transfer of the file before is still listed when this one goes.
    expect(page([{ path: 'usb/a.bgcode', state: 'done' }])).toEqual([
      'b.bgcode:uploading',
    ]);
  });

  it('leaves out a file somebody else is sending', () => {
    const f = fakes();
    f.rows.push(elsewhere({ deviceId: 'p1' }));
    const sends = sendsOf(f);

    expect(pendingSendsOf(sends.rows, 'p1', [])).toEqual([]);
  });
});

describe('printFirst', () => {
  it('sends the file picked to print first and prints only that one', () => {
    expect(printFirst(['a', 'b', 'c'], 2)).toEqual([
      { file: 'c', print: true },
      { file: 'a', print: false },
      { file: 'b', print: false },
    ]);
  });

  it('copies every file in its order when none is printed', () => {
    expect(printFirst(['a', 'b'], undefined)).toEqual([
      { file: 'a', print: false },
      { file: 'b', print: false },
    ]);
  });

  it('prints one of two files of the same name', () => {
    expect(
      printFirst(['part', 'part'], 1).filter((one) => one.print),
    ).toHaveLength(1);
  });
});
