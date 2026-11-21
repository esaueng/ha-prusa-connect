// Every file on its way to a printer the signed-in person may read, whoever
// started it and from whichever browser. Read once and kept current from the
// event stream the fleet already holds.
import type { StreamEvent } from '@prusa/connect';
import { SvelteSet } from 'svelte/reactivity';

import { type DataFault, faultOf } from './fault';
import { deviceIdOf, type Fleet, withdrawnOriginOf } from './fleet.svelte';
import {
  patchOf,
  readRunningTransfers,
  type RunningTransfer,
  type RunningTransfersClient,
  stopTransfer,
  TRANSFER_DONE,
  TRANSFER_EVENTS,
  TRANSFER_RUNNING,
} from './services/transfers';

const CONTINUITY_LOST = 'com.prusa.connect.stream.continuity.lost';
const SETTLED = 'com.prusa.connect.device.transfer.settled';

/** What one transfer moves: a file of a storage to a place on a printer. */
export interface TransferMoved {
  readonly deviceId: string;
  readonly storage: string;
  readonly source: string;
  readonly path: string;
}

const movedKey = (moved: TransferMoved): string =>
  JSON.stringify([moved.deviceId, moved.storage, moved.source, moved.path]);

const sentKey = (
  sent: Pick<TransferMoved, 'deviceId' | 'storage' | 'source'>,
): string => JSON.stringify([sent.deviceId, sent.storage, sent.source]);

/** One transfer still on its way to a printer, as the store keeps it. */
export interface RunningTransferRow {
  readonly transferId: string;
  readonly deviceId: string;
  /** The printer's name as the printer listing shows it. */
  readonly printerName: string;
  /** The storage the bytes come from, on this server. */
  readonly storage: string;
  /** The path inside that storage. */
  readonly source: string;
  /** Where the file lands on the printer, storage-prefixed. */
  readonly path: string;
  readonly bytesTotal: number;
  readonly bytesSent: number;
  /** `queued` or `sending`. */
  readonly state: string;
  /** True when the signed-in person started it, in this tab or any other. */
  readonly mine: boolean;
  /**
   * Who started it, such as `user:ada`. The server names the person only to
   * someone who may read the printer's audit, so it is often absent.
   */
  readonly startedBy: string | undefined;
  /** True when the signed-in person may stop it. */
  readonly canStop: boolean;
}

/**
 * The newest running transfer that moves one file of this server to one
 * printer, from rows kept newest first. Without `path`, a transfer landing
 * anywhere on the printer matches.
 */
export const runningTransferOf = (
  rows: readonly RunningTransferRow[],
  file: {
    readonly deviceId: string;
    readonly storage: string;
    readonly source: string;
    readonly path?: string | undefined;
  },
): RunningTransferRow | undefined =>
  rows.find(
    (row) =>
      row.deviceId === file.deviceId &&
      row.storage === file.storage &&
      row.source === file.source &&
      (file.path === undefined || row.path === file.path),
  );

const rowOf = (transfer: RunningTransfer): RunningTransferRow => ({
  transferId: transfer.transfer_id,
  deviceId: transfer.device_id,
  printerName: transfer.printer_name,
  storage: transfer.storage,
  source: transfer.source,
  path: transfer.path,
  bytesTotal: transfer.bytes_total,
  bytesSent: transfer.bytes_sent,
  state: transfer.state,
  mine: transfer.mine,
  startedBy:
    transfer.started_by === undefined || transfer.started_by === ''
      ? undefined
      : transfer.started_by,
  canStop: transfer.can_stop,
});

/**
 * A shared store, so a class (svelte-5.md). The client comes in through the
 * constructor, which lets a test pass a fake.
 *
 * The listing says who started each transfer and what the person may do
 * about it, and the events say only how the bytes move. So a transfer the
 * store first hears of on the stream is read from the listing, and the
 * frames after it move its bytes. A settled frame drops the row: the store
 * holds what is on its way, never what has arrived.
 */
export class RunningTransfers {
  readonly #client: RunningTransfersClient;
  #rows = $state.raw<readonly RunningTransferRow[]>([]);
  #loading = $state(false);
  #loaded = $state(false);
  #error = $state<DataFault | undefined>(undefined);
  // The transfers a stop was asked for and has not answered.
  readonly #stopping = new SvelteSet<string>();
  // The transfers a settled frame closed. A read that left before the frame
  // may still list one, and a frame that arrives late may still name it.
  // Nothing draws it.
  // eslint-disable-next-line svelte/prefer-svelte-reactivity
  readonly #ended = new Set<string>();
  // The files a send waits to land, by what they move, and those of them a
  // settled frame said landed whole. Only an awaited file is kept, so the set
  // is as large as the sends in flight and a transfer from another tab adds
  // nothing. Nothing draws the first set.
  // eslint-disable-next-line svelte/prefer-svelte-reactivity
  readonly #awaited = new Set<string>();
  readonly #landed = new SvelteSet<string>();
  // What runs once a stored file is on its way to a printer, by device,
  // storage and source. Nothing draws it.
  // eslint-disable-next-line svelte/prefer-svelte-reactivity
  readonly #onItsWay = new Map<string, (path: string) => void>();
  // The transfers a frame named before the store held them, each read for
  // once. Nothing draws it.
  // eslint-disable-next-line svelte/prefer-svelte-reactivity
  readonly #asked = new Set<string>();
  #flight: Promise<void> | undefined;
  // Whether something asked for a read while one was in flight. That read
  // may have left before the transfer it was asked for started.
  #again = false;
  // Counts the resets. A request from before a reset records nothing.
  #resets = 0;

  constructor(client: RunningTransfersClient) {
    this.#client = client;
  }

  /** Newest first. Read-only by design: the collection is the server's. */
  get rows(): readonly RunningTransferRow[] {
    return this.#rows;
  }

  get loading(): boolean {
    return this.#loading;
  }

  /** True once a read has answered, which tells "nothing on its way" from "not read". */
  get loaded(): boolean {
    return this.#loaded;
  }

  get error(): DataFault | undefined {
    return this.#error;
  }

  /** One row by its id, or undefined for a transfer that is not on its way. */
  transfer(transferId: string): RunningTransferRow | undefined {
    return this.#rows.find((row) => row.transferId === transferId);
  }

  /** How far one transfer has got, from 0 to 1. Null while its length is unknown. */
  progress(transferId: string): number | null {
    const row = this.transfer(transferId);
    if (row === undefined || row.bytesTotal <= 0) return null;

    return Math.min(row.bytesSent / row.bytesTotal, 1);
  }

  /** True once a transfer of this file to this place landed it whole. */
  landedAt(moved: TransferMoved): boolean {
    return this.#landed.has(movedKey(moved));
  }

  /**
   * Waits for a file to land, forgetting an earlier landing of the same file
   * to the same place, so a new send of it starts unknown.
   */
  awaitLanding(moved: TransferMoved): void {
    const key = movedKey(moved);
    this.#landed.delete(key);
    this.#awaited.add(key);
  }

  /**
   * Runs `run` once this server is sending the stored file `source` to the
   * printer, or has already landed it there, with the path it lands at: a
   * send that keeps both files lands under another name than it asked for.
   * It follows the event stream and not the answer to the send, which comes
   * only when the transfer has ended. A transfer of that file that fails
   * first drops it, and a second call for the same file replaces the first.
   * A send that starts no transfer is taken back with `notOnItsWay`, or a
   * later send of the same file would run it.
   */
  whenOnItsWay(
    sent: Pick<TransferMoved, 'deviceId' | 'storage' | 'source'>,
    run: (path: string) => void,
  ): void {
    this.#onItsWay.set(sentKey(sent), run);
  }

  /** Forgets who waits for a file to be on its way, for a send that started none. */
  notOnItsWay(
    sent: Pick<TransferMoved, 'deviceId' | 'storage' | 'source'>,
  ): void {
    this.#onItsWay.delete(sentKey(sent));
  }

  /** Stops waiting for a file to land, and forgets that it did. */
  forget(moved: TransferMoved): void {
    const key = movedKey(moved);
    this.#awaited.delete(key);
    this.#landed.delete(key);
  }

  /** True while a stop of this transfer has not answered. */
  stopping(transferId: string): boolean {
    return this.#stopping.has(transferId);
  }

  /** A second call while one read is in flight is given that read. */
  load(): Promise<void> {
    return this.#flight ?? this.#fresh();
  }

  /**
   * Takes every transfer event the fleet applies. The returned function
   * stops it.
   */
  watch(fleet: Pick<Fleet, 'subscribe'>): () => void {
    return fleet.subscribe((event) => {
      void this.apply(event);
    });
  }

  /**
   * Applies one event. It is public so a test drives the store without a
   * socket.
   */
  async apply(event: StreamEvent): Promise<void> {
    if (event.type === CONTINUITY_LOST) {
      await this.#refresh();

      return;
    }
    const gone = withdrawnOriginOf(event);
    if (gone?.kind === 'device') {
      this.#rows = this.#rows.filter((row) => row.deviceId !== gone.id);

      return;
    }
    if (!TRANSFER_EVENTS.includes(event.type)) return;

    const patch = patchOf(event);
    if (patch === undefined) return;
    const id = patch.transferId;

    // Before anything else reads the frame: a transfer this store never
    // listed still tells whoever waits for its file.
    const row = this.transfer(id);
    const sender = row?.deviceId ?? deviceIdOf(event);
    const storage = row?.storage ?? patch.storage;
    const source = row?.source ?? patch.source;
    const landsAt = row?.path ?? patch.path;
    if (
      sender !== undefined &&
      storage !== undefined &&
      source !== undefined &&
      landsAt !== undefined
    ) {
      const key = sentKey({ deviceId: sender, storage, source });
      const waiting = this.#onItsWay.get(key);
      if (waiting !== undefined) {
        const failed = event.type === SETTLED && patch.state !== TRANSFER_DONE;
        this.#onItsWay.delete(key);
        if (!failed) waiting(landsAt);
      }
    }

    if (event.type === SETTLED) {
      this.#ended.add(id);
      const held = this.transfer(id);
      const deviceId = held?.deviceId ?? deviceIdOf(event);
      const storage = held?.storage ?? patch.storage;
      const source = held?.source ?? patch.source;
      const path = held?.path ?? patch.path;
      if (
        patch.state === TRANSFER_DONE &&
        deviceId !== undefined &&
        storage !== undefined &&
        source !== undefined &&
        path !== undefined
      ) {
        const key = movedKey({ deviceId, storage, source, path });
        if (this.#awaited.has(key)) this.#landed.add(key);
      }
      this.#rows = this.#rows.filter((row) => row.transferId !== id);

      return;
    }
    if (this.#ended.has(id)) return;

    const at = this.#rows.findIndex((row) => row.transferId === id);
    const held = this.#rows[at];
    if (held === undefined) {
      // Only the listing says who started it and whether the person may stop
      // it, so the store reads it, once per transfer. A frame relayed from a
      // server joined below names a transfer this server does not list, and
      // its progress frames must not each cost a read.
      if (this.#asked.has(id)) return;
      this.#asked.add(id);
      await this.#refresh();

      return;
    }

    const next = [...this.#rows];
    next[at] = {
      ...held,
      bytesTotal: patch.bytesTotal ?? held.bytesTotal,
      bytesSent: patch.bytesSent ?? held.bytesSent,
      state:
        patch.state !== undefined && TRANSFER_RUNNING.includes(patch.state)
          ? patch.state
          : held.state,
    };
    this.#rows = next;
  }

  /**
   * Stops one transfer. The row leaves when the settled frame arrives, so a
   * stop the printer has not acted on yet keeps it.
   */
  async stop(transferId: string): Promise<void> {
    const row = this.transfer(transferId);
    if (row === undefined || this.#stopping.has(transferId)) return;

    const resets = this.#resets;
    this.#error = undefined;
    this.#stopping.add(transferId);
    try {
      await stopTransfer(this.#client, row.deviceId, transferId);
    } catch (thrown) {
      if (resets === this.#resets) this.#error = faultOf(thrown);
    } finally {
      if (resets === this.#resets) this.#stopping.delete(transferId);
    }
  }

  /**
   * Forgets every transfer this store read. The app calls it when the person
   * signed in changes, so the next person never sees what the last one could.
   */
  reset(): void {
    this.#resets += 1;
    this.#flight = undefined;
    this.#again = false;
    this.#rows = [];
    this.#ended.clear();
    this.#landed.clear();
    this.#awaited.clear();
    this.#onItsWay.clear();
    this.#asked.clear();
    this.#stopping.clear();
    this.#loading = false;
    this.#loaded = false;
    this.#error = undefined;
  }

  // Whether something asked for a read while the last one was in flight. A
  // method, because the field changes across the await in the read.
  #askedAgain(): boolean {
    return this.#again;
  }

  // Reads the listing again. A read already in flight may have left before
  // what this was asked for, so it is followed by one more read.
  #refresh(): Promise<void> {
    if (this.#flight === undefined) return this.#fresh();
    this.#again = true;

    return this.#flight;
  }

  // Starts a read of its own, which any later load() call joins.
  #fresh(): Promise<void> {
    const flight = this.#read().finally(() => {
      if (this.#flight === flight) this.#flight = undefined;
    });
    this.#flight = flight;

    return flight;
  }

  async #read(): Promise<void> {
    const resets = this.#resets;
    this.#loading = true;
    try {
      do {
        this.#again = false;
        const page = await readRunningTransfers(this.#client);
        if (resets !== this.#resets) return;
        this.#rows = (page.items ?? [])
          .filter((one) => !this.#ended.has(one.transfer_id))
          .map(rowOf);
        this.#loaded = true;
        this.#error = undefined;
      } while (this.#askedAgain());
    } catch (thrown) {
      if (resets === this.#resets) this.#error = faultOf(thrown);
    } finally {
      if (resets === this.#resets) this.#loading = false;
    }
  }
}

/** One store for the app, built where the application builds its `PrusaConnect`. */
export const createRunningTransfers = (
  client: RunningTransfersClient,
): RunningTransfers => new RunningTransfers(client);
