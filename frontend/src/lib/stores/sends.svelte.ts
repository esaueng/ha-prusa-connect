// The files on their way to a printer. This tab's own files are uploaded to
// this server first, then sent from there to the printer, and printed there
// when the person asked for that. A file the person
// keeps is saved into a storage folder they chose. Any other file goes up as
// a temporary copy, which the server deletes once it is sent.
//
// Beside them it draws every other file the server is sending to a printer
// the person may read, whoever started it and from whichever browser, so two
// people looking at the same fleet see the same files on their way.
//
// One store for the app, so a file dropped on a printer page keeps going
// when the person moves to another page, and the shell can draw every file
// on its way wherever the person is.
import type {
  RunningTransferRow,
  RunningTransfers,
  PrintEnd,
  SavedObject,
  Storage,
  TransferMoved,
  Transfers,
} from '@prusa/connect-ui/data';
import { runningTransferOf, TRANSFER_RUNNING } from '@prusa/connect-ui/data';

/** What this store reads of the Storage store. */
export type SendsStorage = Pick<
  Storage,
  'upload' | 'uploads' | 'cancelUpload' | 'uploadTemporary'
>;

/** What this store reads of one printer's Transfers store. */
export type SendsTransfers = Pick<
  Transfers,
  'send' | 'print' | 'error' | 'waiting'
>;

/** What this store reads of the store of every running transfer. */
export type SendsRunning = Pick<
  RunningTransfers,
  | 'rows'
  | 'progress'
  | 'landedAt'
  | 'awaitLanding'
  | 'whenOnItsWay'
  | 'notOnItsWay'
  | 'forget'
  | 'stop'
  | 'stopping'
  | 'load'
>;

/**
 * Where one file still going somewhere is: on its way to this server, on this
 * server waiting for its printer to finish the file before it, waiting for the
 * person to answer about a name the printer holds, on this server while it
 * asks the printer about the name or deletes the printer's file to replace
 * it, or on its way from this server to the printer. A row is `'checking'`
 * or `'replacing'` while its job is sending and no transfer for it is listed
 * yet.
 */
export type SendStep =
  'uploading' | 'waiting' | 'asking' | 'checking' | 'replacing' | 'sending';

/**
 * What a send ended with. A job ended by `reset()` answers `'cancelled'`.
 * `'taken'` is a printer that holds a file of that name: the printer's page
 * asks the person whether to replace it. `'refused'` is a file that landed
 * and that the printer would not start: its reason is the printer store's
 * error.
 */
export type SendOutcome = 'done' | 'failed' | 'cancelled' | 'taken' | 'refused';

export interface SendRow {
  /** Stable while the row is drawn, and what `cancel` takes. */
  readonly id: string;
  readonly deviceId: string;
  /** The printer's name. */
  readonly printerName: string;
  readonly name: string;
  /** Where the file lands on the printer, such as `usb/cube.bgcode`. */
  readonly path: string;
  readonly step: SendStep;
  /** From 0 to 1 within the current step. Null while the length is unknown. */
  readonly progress: number | null;
  /** True when the signed-in person started it, in this tab or another. */
  readonly mine: boolean;
  /** Who started it, such as `user:ada`, when the server names the person. */
  readonly startedBy: string | undefined;
  /** True when the person may stop it, which `cancel` then does. */
  readonly canCancel: boolean;
  /** True while a stop asked for has not answered. */
  readonly stopping: boolean;
}

interface Job {
  readonly id: number;
  readonly deviceId: string;
  readonly printerName: string;
  readonly storage: string;
  readonly name: string;
  /**
   * Where the file is in the storage. For a kept file it is known before the
   * upload; for a temporary copy the server answers it, and it is empty
   * until then.
   */
  readonly source: string;
  /** Whether the file is saved in the storage, or only a temporary copy. */
  readonly kept: boolean;
  readonly destination: string;
  /** True when the printer starts the file once it lands. */
  readonly print: boolean;
  readonly step: SendStep;
  /** True once the person chose to replace the printer's file. */
  readonly replacing?: boolean;
  /** The bytes of a temporary copy sent so far, and of the whole file. */
  readonly sent: number;
  readonly total: number;
}

const nameOf = (path: string): string => path.split('/').pop() ?? path;

/** What a job's transfer moves, as the store of running transfers names it. */
const movedOf = (job: Job): TransferMoved => ({
  deviceId: job.deviceId,
  storage: job.storage,
  source: job.source,
  path: job.destination,
});

// How often a file waiting for a free printer has the running transfers read
// again.
const RECHECK_MS = 30_000;

// Whether the person is still asked about the name this job's file met on
// its printer.
const isAsked = (job: Job, transfers: SendsTransfers): boolean =>
  transfers.waiting.some(
    (one) =>
      one.storage === job.storage &&
      one.source === job.source &&
      one.path === job.destination,
  );

/** A path on a printer, the same whether or not it starts with a slash. */
const onPrinter = (path: string): string => path.replace(/^\/+/, '');

/**
 * This tab's files for one printer that its page draws as its own rows,
 * oldest first, until the transfer that moves one is listed in `moving`.
 * An ended transfer to the same path stands for the file only once the file
 * has landed: before then it is an earlier send of the same name.
 */
export const pendingSendsOf = (
  rows: readonly SendRow[],
  deviceId: string,
  moving: readonly { readonly path: string; readonly state: string }[],
): SendRow[] =>
  rows
    .filter(
      (row) =>
        row.deviceId === deviceId &&
        row.id.startsWith('job:') &&
        (row.step === 'uploading' ||
          row.step === 'waiting' ||
          row.step === 'asking' ||
          !moving.some(
            (one) =>
              onPrinter(one.path) === onPrinter(row.path) &&
              (TRANSFER_RUNNING.includes(one.state) || row.progress === 1),
          )),
    )
    .reverse();

/**
 * The files of one drop in the order they go to the printer, each with
 * whether it is printed: the one at `print` goes first and is the only one
 * printed, and the rest keep their order and are copied. `print` is a place
 * among `files`, so two files of the same name stay two.
 */
export const printFirst = <T>(
  files: readonly T[],
  print: number | undefined,
): { file: T; print: boolean }[] => {
  const picked = print === undefined ? undefined : files[print];
  const rest = files.filter((_, at) => at !== print);

  return [
    ...(picked === undefined ? [] : [{ file: picked, print: true }]),
    ...rest.map((file) => ({ file, print: false })),
  ];
};

/** A name inside a folder, where the empty folder is the storage's root. */
const joinPath = (folder: string, name: string): string =>
  folder === '' ? name : `${folder}/${name}`;

/**
 * A shared store, so a class (svelte-5.md). Its inputs come in through the
 * constructor, which lets a test pass fakes.
 *
 * It holds where each file this tab is sending is in the chain. The bytes
 * counted come from the Storage store while a file goes up, and from the
 * store of every running transfer while it goes to the printer, so they are
 * never copied here.
 *
 * The relay from this server to the printer runs on without the page, so a
 * reload loses the tab's jobs and not the relay. The store of every running
 * transfer reads it again from the server, so a file still on its way after
 * a reload is drawn again, in this tab and in every other.
 */
export class Sends {
  readonly #storage: SendsStorage;
  readonly #transfers: (deviceId: string) => SendsTransfers;
  readonly #running: SendsRunning;
  #jobs = $state.raw<readonly Job[]>([]);
  // The upload running for each storage and name, with the file it carries.
  // Every printer page uploads into the same storage under the file's own
  // name, and the Storage store aborts an upload when a second one of the
  // same key starts. So a second send of the same file still going up waits
  // for the same upload. A different file of that name is uploaded, and the
  // send that waited for the older upload ends cancelled. It is not reactive,
  // because nothing draws it.
  // eslint-disable-next-line svelte/prefer-svelte-reactivity
  readonly #flights = new Map<
    string,
    { file: File; flight: Promise<SavedObject | undefined> }
  >();
  // The way to stop each temporary copy still going up, by job. A temporary
  // copy is never shared: the server deletes it after one send, so a second
  // printer gets a copy of its own. It is not reactive, because nothing
  // draws it.
  // eslint-disable-next-line svelte/prefer-svelte-reactivity
  readonly #aborts = new Map<number, AbortController>();
  // The last send to each printer, by printer. A Buddy board fetches one file
  // at a time and refuses a second as busy, so the sends to one printer go one
  // after another. It is not reactive, because nothing draws it.
  // eslint-disable-next-line svelte/prefer-svelte-reactivity
  readonly #lanes = new Map<string, Promise<void>>();
  // The place in its printer's line that a file the printer holds under its
  // name keeps while the person is asked about it, by printer. Replacing the
  // file goes in that place, before the files dropped after it. It is not
  // reactive, because nothing draws it.
  // eslint-disable-next-line svelte/prefer-svelte-reactivity
  readonly #held = new Map<string, { jobId: number; handOn: () => void }>();
  #next = 0;
  // Counts the resets. A chain that started before a reset records nothing.
  #resets = 0;

  constructor(
    storage: SendsStorage,
    transfers: (deviceId: string) => SendsTransfers,
    running: SendsRunning,
  ) {
    this.#storage = storage;
    this.#transfers = transfers;
    this.#running = running;
  }

  /**
   * Every file still on its way to this server or to a printer: this tab's
   * own sends, newest first, then every other transfer the server is running
   * to a printer the person may read, newest first.
   */
  readonly rows: readonly SendRow[] = $derived.by(() => {
    // A transfer one of the jobs is waiting for is drawn as that job. The set
    // lives for this one computation, so nothing reads it reactively.
    // eslint-disable-next-line svelte/prefer-svelte-reactivity
    const claimed = new Set<string>();
    const own = this.#jobs.map((job): SendRow => {
      const relay = job.step === 'sending' ? this.#transferOf(job) : undefined;
      if (relay !== undefined) claimed.add(relay.transferId);
      // Sending, and no transfer opened yet: the server is asking the printer
      // about the name, or deleting the printer's file to replace it.
      const opening =
        job.step === 'sending' &&
        relay === undefined &&
        !this.#running.landedAt(movedOf(job));
      const before = job.replacing === true ? 'replacing' : 'checking';

      return {
        id: `job:${String(job.id)}`,
        deviceId: job.deviceId,
        printerName: job.printerName,
        name: job.name,
        path: job.destination,
        step: opening ? before : job.step,
        progress: this.#progressOf(job, relay),
        mine: true,
        startedBy: undefined,
        canCancel:
          (job.step !== 'sending' && job.step !== 'asking') ||
          relay?.canStop === true,
        stopping:
          relay !== undefined && this.#running.stopping(relay.transferId),
      };
    });

    const relayed = this.#running.rows
      .filter((row) => !claimed.has(row.transferId))
      .map((row): SendRow => ({
        id: `transfer:${row.transferId}`,
        deviceId: row.deviceId,
        printerName: row.printerName,
        name: nameOf(row.path),
        path: row.path,
        step: 'sending',
        progress: this.#running.progress(row.transferId),
        mine: row.mine,
        startedBy: row.startedBy,
        canCancel: row.canStop,
        stopping: this.#running.stopping(row.transferId),
      }));

    return [...own, ...relayed];
  });

  /** True while a file is still on its way to this server or to a printer. */
  get active(): boolean {
    return this.rows.length > 0;
  }

  /**
   * True while the browser still holds a file's bytes, or a file waits here
   * for its printer. Closing the tab then loses the file, which is what the
   * shell asks the person about.
   */
  get uploading(): boolean {
    return this.#jobs.some((job) => job.step !== 'sending');
  }

  /**
   * Uploads one file into `storage`, then sends it to the printer at
   * `directory`. The job leaves `rows` the moment it ends, and the promise
   * resolves to the outcome it ended with. A job ended by `reset()` resolves
   * to `'cancelled'`.
   *
   * A printer fetches one file at a time, so the sends to one printer go one
   * after another, in the order they were asked for. A file whose turn has
   * not come is `'waiting'`, and `cancel` takes it out of the line.
   *
   * `keep` is the folder of `storage` the file is saved in, and the empty
   * string is its root. Without it the file goes up as a temporary copy,
   * which the server deletes once the file has landed or the transfer was
   * stopped, and keeps for a while after a failed send so the printer page
   * can send it again. A temporary copy goes up only once its turn has come,
   * because the server also deletes one nobody has sent for an hour.
   *
   * The same kept file already going up to the same place is not uploaded
   * again: this send waits for that upload, then sends to its own printer. A
   * file is the same when its name, size and last change time match.
   *
   * `print` starts the file once it lands. The server sends it and starts it
   * in one request, so the print goes on when the page is closed. A start
   * the printer refuses answers `'refused'` and hands how it ended, with the
   * printer's fault, to `onRefused`.
   *
   * `onItsWay` runs once this server is sending the file to the printer,
   * with the path it lands at, which is another name when the person kept
   * both files. It does not wait for the landing, and a send that never
   * starts never runs it.
   */
  async send(
    file: File,
    deviceId: string,
    printerName: string,
    storage: string,
    directory: string,
    {
      keep,
      print = false,
      onRefused,
      onItsWay,
    }: {
      keep?: string | undefined;
      print?: boolean;
      onRefused?: (ended: PrintEnd) => void;
      onItsWay?: ((path: string) => void) | undefined;
    } = {},
  ): Promise<SendOutcome> {
    const resets = this.#resets;
    this.#next += 1;
    const kept = keep !== undefined;
    const queued = this.#lanes.has(deviceId) || this.#busy(deviceId);
    let job: Job = {
      id: this.#next,
      deviceId,
      printerName,
      storage,
      name: file.name,
      source: kept ? joinPath(keep, file.name) : '',
      kept,
      destination: `${directory}/${file.name}`,
      print,
      step: queued && !kept ? 'waiting' : 'uploading',
      sent: 0,
      total: file.size,
    };
    this.#jobs = [job, ...this.#jobs];

    // The place in the printer's line is taken now, so the files go to the
    // printer in the order they were asked for and not in the order their
    // uploads end.
    const turn = this.#turn(job);
    const transfers = this.#transfers(deviceId);
    if (kept) {
      return this.#upAndDeliver(
        job,
        file,
        transfers,
        resets,
        turn,
        onRefused,
        onItsWay,
      );
    }

    const handOn = turn instanceof Promise ? await turn : turn;
    if (resets !== this.#resets) {
      handOn?.();

      return 'cancelled';
    }
    if (handOn === undefined) {
      this.#end(job.id);

      return 'cancelled';
    }
    job = { ...job, step: 'uploading' };
    this.#put(job);

    return this.#upAndDeliver(
      job,
      file,
      transfers,
      resets,
      handOn,
      onRefused,
      onItsWay,
    );
  }

  // Uploads the file, waits for its turn at the printer when that has not
  // come yet, and sends the file. The turn is handed on however it ends.
  async #upAndDeliver(
    uploading: Job,
    file: File,
    transfers: SendsTransfers,
    resets: number,
    turn: (() => void) | Promise<(() => void) | undefined>,
    onRefused: ((ended: PrintEnd) => void) | undefined,
    onItsWay: ((path: string) => void) | undefined,
  ): Promise<SendOutcome> {
    let handOn = turn instanceof Promise ? undefined : turn;
    let held = false;
    try {
      let job = uploading;
      const { storage, kept } = job;
      const saved = kept
        ? await this.#upload(storage, job.source, file)
        : await this.#uploadTemporary(job, file);
      if (resets !== this.#resets) return 'cancelled';
      if (saved === 'cancelled' || saved === 'failed') {
        this.#end(job.id);

        return saved;
      }
      if (saved === undefined) {
        // The Storage row for this key may now belong to a later upload of
        // the same name from the Storage page, and it still reads "sending":
        // that later upload superseded this one, which counts as a cancel and
        // not a failure. Only a row the Storage store marked failed carries a
        // fault.
        const upload = this.#uploadOf(job);
        this.#end(job.id);

        return upload?.state === 'failed' ? 'failed' : 'cancelled';
      }

      job = { ...job, source: saved.path };
      if (handOn === undefined && turn instanceof Promise) {
        job = { ...job, step: 'waiting' };
        this.#put(job);
        handOn = await turn;
        if (resets !== this.#resets) return 'cancelled';
        if (handOn === undefined) {
          this.#end(job.id);

          return 'cancelled';
        }
      }

      // A transfer somebody started while this file went up is waited for
      // too, and the file can be cancelled meanwhile.
      if (handOn !== undefined && this.#busy(job.deviceId)) {
        job = { ...job, step: 'waiting' };
        this.#put(job);
        const free = await this.#await(job, this.#free(job.deviceId), handOn);
        if (resets !== this.#resets) return 'cancelled';
        if (free === undefined) {
          this.#end(job.id);

          return 'cancelled';
        }
      }

      const outcome = await this.#deliver(
        { ...job, step: 'sending' },
        transfers,
        resets,
        onRefused,
        onItsWay,
      );
      if (outcome === 'taken' && handOn !== undefined) {
        this.#hold(job, transfers, handOn);
        held = true;
      }

      return outcome;
    } finally {
      // A place the question holds is handed on by its answer. A turn that
      // comes after the job ended is handed straight on.
      if (!held) {
        if (handOn !== undefined) handOn();
        else if (turn instanceof Promise) void turn.then((late) => late?.());
      }
    }
  }

  // Keeps the job's place in its printer's line while the person is asked
  // about the name the printer holds. Replacing the file takes the place
  // through inTurn; keeping the printer's file hands it on.
  #hold(job: Job, transfers: SendsTransfers, handOn: () => void): void {
    const { deviceId } = job;
    const asked = (): boolean => isAsked(job, transfers);
    // The file stays drawn while the person is asked, in the place it had
    // among the files of its drop, so it is never gone from the page.
    this.#jobs = [...this.#jobs, { ...job, step: 'asking' as const }].sort(
      (one, other) => other.id - one.id,
    );
    const held = { jobId: job.id, handOn };
    this.#held.set(deviceId, held);
    const stop = $effect.root(() => {
      $effect(() => {
        if (asked()) return;
        if (this.#held.get(deviceId) === held) {
          this.#held.delete(deviceId);
          this.#end(job.id);
          handOn();
        }
        queueMicrotask(stop);
      });
    });
  }

  // Sends a file that is on this server to its printer, and prints it when
  // the job says so.
  async #deliver(
    job: Job,
    transfers: SendsTransfers,
    resets: number,
    onRefused: ((ended: PrintEnd) => void) | undefined,
    onItsWay: ((path: string) => void) | undefined,
  ): Promise<SendOutcome> {
    const { storage, deviceId } = job;
    // A landing of the same file to the same place from an earlier send is
    // not this one's.
    this.#running.awaitLanding(movedOf(job));
    if (onItsWay !== undefined) {
      this.#running.whenOnItsWay(
        { deviceId, storage, source: job.source },
        onItsWay,
      );
    }
    this.#put(job);

    // A send that started no transfer takes its wait back, so a later send
    // of the same file does not run it.
    const notSent = (end: SendOutcome): SendOutcome => {
      if (onItsWay !== undefined) {
        this.#running.notOnItsWay({ deviceId, storage, source: job.source });
      }

      return end;
    };

    if (job.print) {
      const printed = await transfers.print(
        storage,
        job.source,
        job.destination,
      );
      if (resets !== this.#resets) return 'cancelled';
      this.#end(job.id);
      if (printed?.answer?.ok === true) return 'done';
      if (printed?.answer !== undefined) {
        onRefused?.(printed);

        return notSent('refused');
      }
    } else {
      const sent = await transfers.send(storage, job.source, job.destination);
      if (resets !== this.#resets) return 'cancelled';
      this.#end(job.id);
      if (sent !== undefined) return 'done';
    }

    // A name the printer holds is not a failure: the send waits in the
    // printer's store for the person's answer, and the printer page asks.
    if (isAsked(job, transfers)) return 'taken';

    // A transfer stopped on the printer page answers the send with an
    // error too. `sendFailure` in `internal/app/devicefiles.go` marks it
    // with the parameter `event` set to "stopped", which is not a failure.
    const stopped = transfers.error?.params.event === 'stopped';

    return notSent(stopped ? 'cancelled' : 'failed');
  }

  /**
   * Runs `work` once every send to the printer asked for before it has ended,
   * and holds the printer until `work` ends. A file the printer holds under
   * a name goes through it when the person replaces it: it takes the place
   * its send kept while the person was asked, ahead of the files dropped
   * after it. It answers undefined when the person cancelled it before
   * `work` ran.
   */
  async inTurn<T>(
    deviceId: string,
    work: () => Promise<T>,
  ): Promise<T | undefined> {
    const held = this.#held.get(deviceId);
    if (held !== undefined) {
      this.#held.delete(deviceId);
      const asking = this.#jobs.find((job) => job.id === held.jobId);
      if (asking === undefined) {
        try {
          await this.#free(deviceId);

          return await work();
        } finally {
          held.handOn();
        }
      }

      // A replace that meets a transfer started elsewhere waits like any
      // file, and a cancel then leaves the printer's file and hands on.
      const waiting = { ...asking, step: 'waiting' as const };
      this.#put(waiting);
      const turn = await this.#await(
        waiting,
        this.#free(deviceId),
        held.handOn,
      );
      if (turn === undefined) {
        this.#end(held.jobId);

        return undefined;
      }
      this.#put({ ...waiting, step: 'sending', replacing: true });
      try {
        return await work();
      } finally {
        this.#end(held.jobId);
        turn();
      }
    }

    const { before, handOn } = this.#queue(deviceId);
    try {
      await before;
      await this.#free(deviceId);

      return await work();
    } finally {
      handOn();
    }
  }

  /**
   * Stops one file. A file still going up is cancelled in the browser. A file
   * on its way to the printer is stopped on the server, whoever started it,
   * when the person may stop it.
   *
   * Two sends that share one upload are stopped together: the upload is
   * cancelled, and both rows end cancelled.
   */
  cancel(id: string): void {
    const row = this.rows.find((held) => held.id === id);
    if (row?.canCancel !== true) return;

    const job = this.#jobs.find((held) => `job:${String(held.id)}` === id);
    if (job === undefined) {
      void this.#running.stop(id.slice('transfer:'.length));

      return;
    }
    if (job.step === 'waiting') {
      this.#aborts.get(job.id)?.abort();

      return;
    }
    if (job.step === 'sending') {
      const relay = this.#transferOf(job);
      if (relay !== undefined) void this.#running.stop(relay.transferId);

      return;
    }
    if (job.kept) this.#storage.cancelUpload(job.storage, job.source);
    else this.#aborts.get(job.id)?.abort();
  }

  /**
   * Forgets every row this tab holds. The app calls it when the person signed
   * in changes. The Storage store and the store of every running transfer
   * are reset with it, and the uploads on their way are aborted. A send
   * still waiting on one of them resolves to `'cancelled'`.
   */
  reset(): void {
    this.#resets += 1;
    this.#jobs = [];
    this.#flights.clear();
    for (const flight of this.#aborts.values()) flight.abort();
    this.#aborts.clear();
    for (const held of this.#held.values()) held.handOn();
    this.#held.clear();
  }

  #upload(
    storage: string,
    path: string,
    file: File,
  ): Promise<SavedObject | undefined> {
    const key = `${storage}/${path}`;
    const running = this.#flights.get(key);
    if (
      running?.file.size === file.size &&
      running.file.lastModified === file.lastModified
    )
      return running.flight;

    const flight = this.#storage.upload(storage, path, file);
    this.#flights.set(key, { file, flight });
    const forget = (): void => {
      if (this.#flights.get(key)?.flight === flight) this.#flights.delete(key);
    };
    void flight.then(forget, forget);

    return flight;
  }

  // Sends one temporary copy up, with its progress kept on the job. It
  // answers what the server saved, or how the upload ended when it did not.
  async #uploadTemporary(
    job: Job,
    file: File,
  ): Promise<SavedObject | 'cancelled' | 'failed'> {
    const flight = new AbortController();
    this.#aborts.set(job.id, flight);
    try {
      const answer = await this.#storage.uploadTemporary(
        job.storage,
        file.name,
        file,
        (sent, total) => {
          const held = this.#jobs.find((one) => one.id === job.id);
          if (held?.step === 'uploading') this.#put({ ...held, sent, total });
        },
        flight.signal,
      );
      if ('saved' in answer) return answer.saved;

      return flight.signal.aborted ? 'cancelled' : 'failed';
    } finally {
      this.#aborts.delete(job.id);
    }
  }

  // Takes the job's place in its printer's line. A free printer answers at
  // once, so a cancel never falls between the row and its abort; otherwise
  // it answers once every earlier send ended, or undefined when cancelled.
  #turn(job: Job): (() => void) | Promise<(() => void) | undefined> {
    const { before, handOn } = this.#queue(job.deviceId);
    if (before === undefined && !this.#busy(job.deviceId)) return handOn;

    const ready = (before ?? Promise.resolve()).then(() =>
      this.#free(job.deviceId),
    );

    return this.#await(job, ready, handOn);
  }

  // Whether the server is running a transfer to the printer that this tab's
  // line does not know of: one another tab or another person started.
  #busy(deviceId: string): boolean {
    return this.#running.rows.some((row) => row.deviceId === deviceId);
  }

  // Settles once the server runs no transfer to the printer, so a file waits
  // for one started elsewhere instead of meeting it as busy. A transfer leaves
  // the running ones on its settled frame, and a frame lost without a
  // continuity event would hold the line for good, so the running transfers
  // are read again every RECHECK_MS while a file waits.
  #free(deviceId: string): Promise<void> {
    if (!this.#busy(deviceId)) return Promise.resolve();

    return new Promise((resolve) => {
      const recheck = setInterval(() => {
        void this.#running.load();
      }, RECHECK_MS);
      const stop = $effect.root(() => {
        $effect(() => {
          if (this.#busy(deviceId)) return;
          clearInterval(recheck);
          resolve();
          queueMicrotask(stop);
        });
      });
    });
  }

  // Puts one more send at the end of the printer's line. `before` settles
  // once every send ahead has ended, and is undefined for a free printer;
  // `handOn` lets the next one go.
  #queue(deviceId: string): {
    before: Promise<void> | undefined;
    handOn: () => void;
  } {
    const before = this.#lanes.get(deviceId);
    let handOn = (): void => undefined;
    const mine = new Promise<void>((resolve) => {
      handOn = resolve;
    });
    const last = before === undefined ? mine : before.then(() => mine);
    this.#lanes.set(deviceId, last);
    void last.then(() => {
      if (this.#lanes.get(deviceId) === last) this.#lanes.delete(deviceId);
    });

    return { before, handOn };
  }

  // Waits for the send before this one, or for the person to cancel this one.
  async #await(
    job: Job,
    before: Promise<void>,
    handOn: () => void,
  ): Promise<(() => void) | undefined> {
    const flight = new AbortController();
    this.#aborts.set(job.id, flight);
    const cancelled = new Promise<void>((resolve) => {
      flight.signal.addEventListener('abort', () => {
        resolve();
      });
    });
    try {
      await Promise.race([before, cancelled]);
    } finally {
      this.#aborts.delete(job.id);
    }
    if (!flight.signal.aborted) return handOn;

    handOn();

    return undefined;
  }

  // The newest running transfer that moves this job's file to its printer.
  #transferOf(job: Job): RunningTransferRow | undefined {
    return runningTransferOf(this.#running.rows, {
      deviceId: job.deviceId,
      storage: job.storage,
      source: job.source,
      path: job.destination,
    });
  }

  #put(job: Job): void {
    this.#jobs = this.#jobs.map((held) => (held.id === job.id ? job : held));
  }

  /** Drops the job that just ended. */
  #end(id: number): void {
    // The landing a sending job waited on is no longer anybody's.
    const ended = this.#jobs.find((held) => held.id === id);
    if (ended?.step === 'sending') this.#running.forget(movedOf(ended));
    this.#jobs = this.#jobs.filter((held) => held.id !== id);
  }

  #uploadOf(job: Job) {
    return this.#storage.uploads.find(
      (row) => row.storage === job.storage && row.path === job.source,
    );
  }

  #progressOf(job: Job, relay: RunningTransferRow | undefined): number | null {
    if (job.step === 'waiting' || job.step === 'asking') return null;
    if (job.step === 'uploading' && !job.kept) {
      return job.total > 0 ? Math.min(job.sent / job.total, 1) : null;
    }
    if (job.step === 'uploading') {
      const upload = this.#uploadOf(job);
      if (upload === undefined || upload.total <= 0) return null;

      return Math.min(upload.sent / upload.total, 1);
    }
    if (relay !== undefined) return this.#running.progress(relay.transferId);

    // The printer has the file and the server has not answered yet.
    const landed = this.#running.landedAt(movedOf(job));

    return landed ? 1 : null;
  }
}
