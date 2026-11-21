// One printer's file transfers, read once and kept current from the event
// stream.
import type { EventSubscription, StreamEvent } from '@prusa/connect';
import { SvelteSet } from 'svelte/reactivity';

import type { PrintOutcome, PrintRequest } from '../domain/actions';
import { type DataFault, faultOf } from './fault';
import { FILE_NAME_TAKEN, printFile, refusalOf } from './services/print';
import {
  patchOf,
  TRANSFER_DONE,
  readTransfers,
  rowOf,
  type SentFile,
  sendFile,
  stopTransfer,
  TRANSFER_EVENTS,
  TRANSFER_RUNNING,
  type TransferPatch,
  type TransferRow,
  type TransfersClient,
} from './services/transfers';

// A frame that says a transfer is moving never reopens a row that ended: the
// answer to a send can arrive before the started frame behind it.
const stateOf = (row: TransferRow, patch: TransferPatch): string => {
  const next = patch.state ?? row.state;

  return TRANSFER_RUNNING.includes(next) &&
    !TRANSFER_RUNNING.includes(row.state)
    ? row.state
    : next;
};

const merged = (row: TransferRow, patch: TransferPatch): TransferRow => ({
  transferId: row.transferId,
  storage: patch.storage ?? row.storage,
  source: patch.source ?? row.source,
  path: patch.path ?? row.path,
  bytesTotal: patch.bytesTotal ?? row.bytesTotal,
  bytesSent: patch.bytesSent ?? row.bytesSent,
  state: stateOf(row, patch),
  reason: patch.reason ?? row.reason,
  startedAt: row.startedAt,
  endedAt: row.endedAt,
});

const opened = (patch: TransferPatch): TransferRow => ({
  transferId: patch.transferId,
  storage: patch.storage ?? '',
  source: patch.source ?? '',
  path: patch.path ?? '',
  bytesTotal: patch.bytesTotal ?? 0,
  bytesSent: patch.bytesSent ?? 0,
  state: patch.state ?? 'queued',
  reason: patch.reason,
});

/** How the store is built beyond its client and printer. */
export interface TransfersOptions {
  /**
   * The person the store reads for, such as `user:ada`. The event stream is
   * reopened after a drop only while the cookie still belongs to them.
   */
  readonly principal?: string;
}

/** A send waiting for the person to say whether to replace the printer's file. */
export interface TakenName {
  readonly storage: string;
  readonly source: string;
  /** Where the file lands on the printer, storage-prefixed. */
  readonly path: string;
  /** True when the file is to be printed once it lands, not only sent. */
  readonly print?: boolean | undefined;
  /**
   * True when the printer lists its file under that name read-only and
   * refuses to delete it, so it cannot be replaced now.
   */
  readonly readOnly?: boolean | undefined;
}

/**
 * How one print ended: the printer's answer to the start, and the fault the
 * file was not printed on. A refused start has both, a request that failed
 * before the printer answered has the fault alone.
 */
export interface PrintEnd {
  readonly answer?: PrintOutcome | undefined;
  readonly fault?: DataFault | undefined;
}

/** A listener the store calls with the path a file landed on. */
export type LandedListener = (path: string) => void;

/**
 * A shared store, so a class (svelte-5.md). The client comes in through the
 * constructor, which lets a test pass a fake.
 *
 * One store per printer. The rows are folded from the listing and the three
 * transfer events, and the two converge whichever order they arrive in: a
 * started frame for a transfer the listing already answered updates that row
 * rather than adding a second.
 *
 * The rows are newest first, the order the listing answers in. A transfer
 * this store learns of later is newer than every row it holds, so it goes
 * first.
 */
export class Transfers {
  readonly #client: TransfersClient;
  readonly #deviceId: string;
  readonly #principal: string | undefined;
  // Several pages share one store, so each registers its own listener.
  #landedListeners: readonly LandedListener[] = [];

  #rows = $state.raw<readonly TransferRow[]>([]);
  // The ids of every transfer this store has held while it was still moving.
  // The listing answers the printer's whole history, and a transfer that ended
  // before this store was built is history rather than news.
  readonly #watched = new SvelteSet<string>();
  #loading = $state(false);
  #loaded = $state(false);
  #error = $state<DataFault | undefined>(undefined);
  // Sends the printer refused for a name it holds, oldest first. Several files
  // dropped at once can each meet one, and each is asked about in turn.
  #taken = $state.raw<readonly TakenName[]>([]);
  #flight: Promise<void> | undefined;
  #subscription: EventSubscription | undefined;
  // How many listeners share the one subscription. Pages share this store,
  // so the stream closes when the last of them closes.
  #listeners = 0;
  // Counts the reads, so an older read that answers late changes nothing.
  #reads = 0;
  // Counts the resets. A request or a stream from before a reset records
  // nothing.
  #resets = 0;

  constructor(
    client: TransfersClient,
    deviceId: string,
    options: TransfersOptions = {},
  ) {
    this.#client = client;
    this.#deviceId = deviceId;
    this.#principal = options.principal;
  }

  /**
   * Registers a listener the store calls with the path a file landed on,
   * when its transfer ends with the state done. The returned function
   * removes the listener again.
   */
  onLanded(listener: LandedListener): () => void {
    this.#landedListeners = [...this.#landedListeners, listener];

    return () => {
      this.#landedListeners = this.#landedListeners.filter(
        (held) => held !== listener,
      );
    };
  }

  #landed(path: string): void {
    for (const listener of this.#landedListeners) listener(path);
  }

  /** The printer this store is about. Read-only by design. */
  get deviceId(): string {
    return this.#deviceId;
  }

  /** Read-only by design: the collection is the server's (svelte-5.md). */
  get rows(): readonly TransferRow[] {
    return this.#rows;
  }

  get loading(): boolean {
    return this.#loading;
  }

  /** True once a read has answered, which tells an idle printer from an unread one. */
  get loaded(): boolean {
    return this.#loaded;
  }

  get error(): DataFault | undefined {
    return this.#error;
  }

  /**
   * The send waiting for the person to say whether to replace the file the
   * printer holds under its name. They answer through replaceTaken,
   * bothTaken and keepTaken.
   */
  get taken(): TakenName | undefined {
    return this.#taken[0];
  }

  /**
   * Every send waiting for the person's answer, oldest first. `taken` is the
   * first of them, the one asked about now.
   */
  get waiting(): readonly TakenName[] {
    return this.#taken;
  }

  /**
   * Sends the waiting file again, replacing the printer's file. It ends as
   * the send or print does, in `error` when that fails, and answers how it
   * ended: the fault of a send or print that failed, with what the printer
   * made of the start when the file was to be printed.
   *
   * The question goes at once, and the file goes through `inTurn`, which a
   * caller that queues the files for this printer passes, so the file waits
   * for a transfer already going to the printer. An `inTurn` that answers
   * undefined was cancelled, and the replace ends with nothing to say.
   */
  async replaceTaken(
    inTurn: <T>(work: () => Promise<T>) => Promise<T | undefined> = (work) =>
      work(),
  ): Promise<PrintEnd | undefined> {
    const [waiting, ...rest] = this.#taken;
    this.#taken = rest;
    if (waiting === undefined) return undefined;

    if (waiting.print === true) {
      return inTurn(() =>
        this.print(waiting.storage, waiting.source, waiting.path, true),
      );
    }
    const answered = await inTurn(() =>
      this.#sending(waiting.storage, waiting.source, waiting.path, true),
    );
    const fault = answered?.fault;

    return fault === undefined ? undefined : { fault };
  }

  /**
   * Sends the waiting file again under a free name, such as part[1].bgcode,
   * and leaves the printer's file where it is. It ends as replaceTaken does,
   * and goes through `inTurn` as replaceTaken does.
   */
  async bothTaken(
    inTurn: <T>(work: () => Promise<T>) => Promise<T | undefined> = (work) =>
      work(),
  ): Promise<PrintEnd | undefined> {
    const [waiting, ...rest] = this.#taken;
    this.#taken = rest;
    if (waiting === undefined) return undefined;

    if (waiting.print === true) {
      return inTurn(() =>
        this.print(waiting.storage, waiting.source, waiting.path, false, true),
      );
    }
    const answered = await inTurn(() =>
      this.#sending(waiting.storage, waiting.source, waiting.path, false, true),
    );
    const fault = answered?.fault;

    return fault === undefined ? undefined : { fault };
  }

  /**
   * Leaves the printer's file where it is and prints it, for a send that was
   * to be printed. Nothing is sent. It answers what the printer made of the
   * start, with its refusal in `error`.
   */
  async printTaken(): Promise<PrintEnd | undefined> {
    const [waiting, ...rest] = this.#taken;
    this.#taken = rest;
    if (waiting === undefined) return undefined;

    return this.#printing(
      { deviceId: this.#deviceId, path: waiting.path },
      (thrown) => {
        this.#error = faultOf(thrown);
      },
    );
  }

  /** Leaves the printer's file where it is and drops the waiting send. */
  keepTaken(): void {
    this.#taken = this.#taken.slice(1);
  }

  /** The transfers still moving bytes. */
  readonly inFlight: readonly TransferRow[] = $derived(
    this.#rows.filter((row) => TRANSFER_RUNNING.includes(row.state)),
  );

  /**
   * The transfers still moving, and those this store saw moving before they
   * ended. A transfer that ended before then is left to the history.
   */
  readonly current: readonly TransferRow[] = $derived(
    this.#rows.filter(
      (row) =>
        TRANSFER_RUNNING.includes(row.state) ||
        this.#watched.has(row.transferId),
    ),
  );

  /** One row by its id, or undefined for a transfer this store has not seen. */
  transfer(transferId: string): TransferRow | undefined {
    return this.#rows.find((row) => row.transferId === transferId);
  }

  /** How far one transfer has got, from 0 to 1. Zero when the length is unknown. */
  progress(transferId: string): number {
    const row = this.transfer(transferId);
    if (row === undefined || row.bytesTotal <= 0) return 0;

    return Math.min(row.bytesSent / row.bytesTotal, 1);
  }

  /** A second call while one read is in flight is given that read. */
  load(): Promise<void> {
    return this.#flight ?? this.#fresh();
  }

  /**
   * Opens the stream and applies what it sends. The returned function closes
   * it, and an application calls that on teardown (svelte-testing.md).
   */
  listen(): () => void {
    const resets = this.#resets;
    this.#listeners += 1;
    if (this.#subscription === undefined) {
      const subscription = this.#client.events({
        types: TRANSFER_EVENTS,
        origins: [`device:${this.#deviceId}`],
        ...(this.#principal === undefined
          ? {}
          : { principal: this.#principal }),
      });
      this.#subscription = subscription;
      void this.#consume(subscription);
    }

    let open = true;

    return () => {
      if (!open) return;
      open = false;
      // A reset closed the stream this listener shared and set the count
      // back to zero.
      if (resets !== this.#resets) return;
      this.#listeners -= 1;
      if (this.#listeners > 0) return;

      this.#subscription?.close();
      this.#subscription = undefined;
    };
  }

  /**
   * Applies one event. It is public so a test drives the store without a
   * socket, and so an application that already has a subscription can feed it.
   */
  async apply(event: StreamEvent): Promise<void> {
    if (event.type === 'com.prusa.connect.stream.continuity.lost') {
      // There is no partial recovery from a lost place in the log, so the
      // transfers are read again. A file that landed during the gap is only
      // seen here, so its landing is reported from the new listing.
      // Before the first listing there is nothing to compare with, and every
      // done row in it is history. The read is a fresh one, because a read
      // already in flight may have left before the transfer landed.
      const known = this.#loaded;
      const before = this.#rows;
      await this.#fresh();
      if (!known) return;
      for (const row of this.#rows) {
        const held = before.find((one) => one.transferId === row.transferId);
        if (row.state === TRANSFER_DONE && held?.state !== TRANSFER_DONE) {
          this.#landed(row.path);
        }
      }

      return;
    }

    // The stream is asked for this printer alone. A frame about another
    // printer that arrives anyway is not this store's to apply.
    if (!this.#isMine(event)) return;

    const patch = patchOf(event);
    if (patch === undefined) return;

    const at = this.#rows.findIndex(
      (row) => row.transferId === patch.transferId,
    );
    const held = at < 0 ? undefined : this.#rows[at];
    const row = held === undefined ? opened(patch) : merged(held, patch);
    if (held === undefined) {
      this.#hold([row, ...this.#rows]);
    } else {
      const next = [...this.#rows];
      next[at] = row;
      this.#hold(next);
    }

    if (row.state === TRANSFER_DONE && held?.state !== TRANSFER_DONE) {
      this.#landed(row.path);
    }
  }

  /**
   * Sends one file from a storage on this server to the printer. A name the
   * printer already holds is not an error: the send waits in `taken` for the
   * person's answer.
   */
  async send(
    storage: string,
    source: string,
    path: string,
    replace = false,
    keepBoth = false,
  ): Promise<SentFile | undefined> {
    return (await this.#sending(storage, source, path, replace, keepBoth)).sent;
  }

  // One send request, answered as what was sent or the fault it failed with,
  // so a caller never has to read `error` after the await. One that waits in
  // `taken` or was reset answers neither.
  async #sending(
    storage: string,
    source: string,
    path: string,
    replace: boolean,
    keepBoth = false,
  ): Promise<{ sent?: SentFile; fault?: DataFault | undefined }> {
    const resets = this.#resets;
    this.#error = undefined;
    try {
      const answered = await sendFile(
        this.#client,
        this.#deviceId,
        storage,
        source,
        path,
        replace,
        keepBoth,
      );
      if (resets !== this.#resets) return {};
      const row = rowOf(answered.transfer);
      const held = this.#rows.find((one) => one.transferId === row.transferId);
      this.#hold([
        row,
        ...this.#rows.filter((one) => one.transferId !== row.transferId),
      ]);
      // The server answers once the transfer settled, so this answer can be
      // the first word of a landing; the stream's frame then finds it held.
      if (row.state === TRANSFER_DONE && held?.state !== TRANSFER_DONE) {
        this.#landed(row.path);
      }

      return { sent: answered };
    } catch (thrown) {
      if (resets !== this.#resets) return {};
      this.#failed(thrown, replace || keepBoth, { storage, source, path });

      return { fault: this.#error };
    }
  }

  /**
   * Sends one file from a storage on this server to the printer and starts it
   * once it has landed. The server composes the two, so a person who closes
   * the page still gets the print.
   *
   * It answers like `send`: a name the printer holds waits in `taken`, and
   * replacing it prints. A printer that takes the file and refuses the start
   * answers with `ok` false and is an error with the printer's reason; the
   * file stays on the printer.
   */
  async print(
    storage: string,
    source: string,
    path: string,
    replace = false,
    keepBoth = false,
  ): Promise<PrintEnd | undefined> {
    return this.#printing(
      { deviceId: this.#deviceId, path, storage, source, replace, keepBoth },
      (thrown) => {
        this.#failed(thrown, replace || keepBoth, {
          storage,
          source,
          path,
          print: true,
        });
      },
    );
  }

  // One print request, answered as how it ended. A refusal is the printer's
  // answer and its fault both; a request that failed is the fault alone, and
  // one that waits in `taken` or was reset answers nothing. The fault is the
  // caller's to show, so it never has to read `error` after the await, where
  // another send may already have replaced it.
  async #printing(
    request: PrintRequest,
    thrownBy: (thrown: unknown) => void,
  ): Promise<PrintEnd | undefined> {
    const resets = this.#resets;
    this.#error = undefined;
    try {
      const answer = await printFile(this.#client, request);
      if (resets !== this.#resets) return undefined;
      const fault = refusalOf(answer);
      this.#error = fault;

      return { answer, fault };
    } catch (thrown) {
      if (resets !== this.#resets) return undefined;
      thrownBy(thrown);
      const fault = this.#error;

      return fault === undefined ? undefined : { fault };
    }
  }

  // A send or print the server refused. A name the printer holds waits in
  // `taken` for the person's answer, unless this send already carried one;
  // anything else is the store's error.
  #failed(thrown: unknown, answered: boolean, waiting: TakenName): void {
    const fault = faultOf(thrown);
    if (fault.code === FILE_NAME_TAKEN && !answered) {
      const readOnly = fault.params.read_only === true;
      this.#taken = [...this.#taken, { ...waiting, readOnly }];
    } else {
      this.#error = fault;
    }
  }

  /**
   * Stops one transfer.
   *
   * The read after it goes through load(), so stopping two transfers at once
   * costs one listing rather than two (svelte-data.md).
   */
  async stop(transferId: string): Promise<void> {
    const resets = this.#resets;
    this.#error = undefined;
    try {
      await stopTransfer(this.#client, this.#deviceId, transferId);
      if (resets === this.#resets) await this.load();
    } catch (thrown) {
      if (resets === this.#resets) this.#error = faultOf(thrown);
    }
  }

  /**
   * Forgets every transfer this store read and closes its stream. The app
   * calls it when the person signed in changes, so the next person never sees
   * the transfers of the last one. An answer to a request made before the
   * reset is dropped when it arrives.
   *
   * A failed read keeps the rows of an earlier read. Those rows were read for
   * the same person, because a change of person resets the store first.
   */
  reset(): void {
    this.#resets += 1;
    this.#reads += 1;
    this.#flight = undefined;
    this.#subscription?.close();
    this.#subscription = undefined;
    this.#listeners = 0;
    this.#rows = [];
    this.#watched.clear();
    this.#loading = false;
    this.#loaded = false;
    this.#error = undefined;
    this.#taken = [];
  }

  // The hub writes the origin as the subject, `device:<id>`, and as the last
  // part of the source. A frame relayed from a server below may carry only
  // the source, so that is read when the subject is absent.
  #isMine(event: StreamEvent): boolean {
    const envelope = event.envelope;
    if (envelope === undefined) return true;
    if (envelope.subject !== undefined) {
      return envelope.subject === `device:${this.#deviceId}`;
    }

    return envelope.source.endsWith(`/device/${this.#deviceId}`);
  }

  #hold(rows: readonly TransferRow[]): void {
    this.#rows = rows;
    for (const row of rows) {
      if (TRANSFER_RUNNING.includes(row.state))
        this.#watched.add(row.transferId);
    }
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
    this.#reads += 1;
    const read = this.#reads;
    this.#loading = true;
    try {
      const page = await readTransfers(this.#client, this.#deviceId);
      if (read === this.#reads) {
        this.#hold((page.items ?? []).map(rowOf));
        this.#loaded = true;
        this.#error = undefined;
      }
    } catch (thrown) {
      if (read === this.#reads) this.#error = faultOf(thrown);
    } finally {
      if (read === this.#reads) this.#loading = false;
    }

    // A newer read replaced this one. A caller waiting on this read gets its
    // answer when the rows are the newer read's, not before.
    if (read !== this.#reads) await this.#flight;
  }

  async #consume(subscription: EventSubscription): Promise<void> {
    try {
      for await (const event of subscription) {
        if (this.#subscription !== subscription) return;
        await this.apply(event);
      }
    } catch (thrown) {
      // The server ended the stream: the credential expired, or it refused at
      // reconnect. Both are for a person to see rather than for a retry loop.
      if (this.#subscription === subscription) this.#error = faultOf(thrown);
    } finally {
      // An ended stream is let go, so the next listener opens a new one.
      if (this.#subscription === subscription) this.#subscription = undefined;
    }
  }
}

/** One store per printer, built where the application builds its `PrusaConnect`. */
export const createTransfers = (
  client: TransfersClient,
  deviceId: string,
  options?: TransfersOptions,
): Transfers => new Transfers(client, deviceId, options);
