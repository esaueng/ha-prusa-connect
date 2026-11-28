// Saving, slicing and printing one plate. A class, because the Print button,
// its menu and the status line read the same flow (svelte-5.md). The plate is
// saved as one STL and sliced once per change: a print, a download and Slice
// only reuse the G-code of a plate that has not changed since.
import {
  describeFault,
  type FaultText,
  type MessageParams,
} from '@prusa/connect-ui/i18n';

import type { Job } from '../api';
import { makeSTL, type PlacedMesh } from '../views/prepare-panel/model';
import {
  placedName,
  queueBlockOf,
  type PrepareApi,
} from '../views/prepare-panel/prepare-api';
import type { PrintSettings } from './print-settings';
import { canRecheckQueue, type QueueBlock } from './printers';
import {
  PageClosed,
  PlateChanged,
  type SendGuard,
  type SendSnapshot,
  stillSendable,
} from './sendable';

/** How often a running job is read again. */
export const JOB_POLL_MS = 1000;

/** What the flow is doing now. */
export type FlowStep = 'idle' | 'saving' | 'slicing' | 'sending' | 'queueing';

/** Everything one slice of the plate depends on. */
export interface PlateInput {
  /**
   * Names the plate's parts as they are: the plate's revision, which every
   * change to a part moves on, however small.
   */
  readonly meshKey: string;
  /** The file the plate came from, for the name of the saved STL. */
  readonly name: string;
  readonly meshes: readonly PlacedMesh[];
  readonly storage: string;
  readonly profile: string;
  readonly settings: PrintSettings | undefined;
}

/** A sentence the page shows once a step has ended, as a catalogue key. */
export interface FlowNotice {
  readonly key: string;
  readonly params: MessageParams;
}

const ENDED: ReadonlySet<Job['state']> = new Set([
  'done',
  'failed',
  'canceled',
]);

/** Waits `ms`, or rejects when the signal aborts first. */
const pause = (ms: number, signal: AbortSignal): Promise<void> =>
  new Promise((resolve, reject) => {
    const timer = setTimeout(done, ms);
    function done(): void {
      signal.removeEventListener('abort', stop);
      resolve();
    }
    function stop(): void {
      clearTimeout(timer);
      reject(
        signal.reason instanceof Error ? signal.reason : new Error('aborted'),
      );
    }
    signal.addEventListener('abort', stop, { once: true });
  });

/** A text that changes whenever the G-code of the plate would. */
export const signatureOf = (
  input: Pick<PlateInput, 'meshKey' | 'profile' | 'settings' | 'storage'>,
): string =>
  JSON.stringify([
    input.meshKey,
    input.profile,
    input.settings ?? {},
    input.storage,
  ]);

export class PlateFlow {
  #step = $state<FlowStep>('idle');
  #fault = $state.raw<FaultText | undefined>(undefined);
  #notice = $state.raw<FlowNotice | undefined>(undefined);
  #sliced = $state.raw<{ signature: string; job: Job } | undefined>(undefined);
  #queue = $state.raw<{ printer: string; block: QueueBlock | undefined }>();
  #waiting = $state.raw<Readonly<Record<string, number | undefined>>>({});
  #saved: { meshKey: string; storage: string; path: string } | undefined;
  readonly #api: PrepareApi;
  readonly #signal: AbortSignal;
  readonly #pollMs: number;

  constructor(api: PrepareApi, signal: AbortSignal, pollMs = JOB_POLL_MS) {
    this.#api = api;
    this.#signal = signal;
    this.#pollMs = pollMs;
  }

  get step(): FlowStep {
    return this.#step;
  }

  get busy(): boolean {
    return this.#step !== 'idle';
  }

  get fault(): FaultText | undefined {
    return this.#fault;
  }

  get notice(): FlowNotice | undefined {
    return this.#notice;
  }

  queueBlock(printer: string | undefined): QueueBlock | undefined {
    const queue = this.#queue;
    return queue && queue.printer === printer ? queue.block : 'queueChecking';
  }

  waitingFor(printer: string): number | undefined {
    return Object.hasOwn(this.#waiting, printer)
      ? this.#waiting[printer]
      : undefined;
  }

  async checkQueue(printer: string, selected: AbortSignal): Promise<void> {
    const signal = AbortSignal.any([this.#signal, selected]);
    this.#queue = { printer, block: 'queueChecking' };
    const block = await this.#api.checkQueue(printer, signal);
    if (!signal.aborted) this.#queue = { printer, block };
  }

  /** The finished job of the plate as it is, or undefined once it changed. */
  slicedFor(signature: string): Job | undefined {
    const sliced = this.#sliced;
    return sliced?.signature === signature ? sliced.job : undefined;
  }

  dismiss(): void {
    this.#fault = undefined;
    this.#notice = undefined;
  }

  /** Saves the plate as an STL in the storage and answers its path. */
  async save(input: PlateInput): Promise<string | undefined> {
    return this.#run(async () => {
      const path = await this.#savedPath(input);
      this.#notice = { key: 'slicer.plate.notice.saved', params: { path } };
      return path;
    });
  }

  /** Slices the plate, or answers the job of the plate as it is. */
  async slice(input: PlateInput): Promise<Job | undefined> {
    return this.#run(async () => {
      const job = await this.#slicedJob(input);
      this.#notice = {
        key: 'slicer.plate.notice.sliced',
        params: { path: job.result?.path ?? '' },
      };
      return job;
    });
  }

  /**
   * Slices the plate if it changed, then sends it to an idle printer and
   * starts it. `current` answers the plate and the printer as they are now.
   * It is read when Print is pressed, and the guard the service gets compares
   * it again after every wait, before anything is sent. When the plate, its
   * settings, the bed or the printer changed, or the printer no longer takes
   * the plate, nothing is sent and the notice says so.
   */
  async print(
    input: PlateInput,
    printer: { id: string; name: string },
    current: () => SendSnapshot,
  ): Promise<boolean> {
    const pressed = current();
    const guard: SendGuard = () => {
      this.#live();
      if (!stillSendable(pressed, current())) throw new PlateChanged();
    };
    const ok = await this.#run(async () => {
      const job = await this.#slicedJob(input);
      const result = job.result;
      if (!result) throw new Error('The job wrote no G-code');
      guard();
      this.#step = 'sending';
      await this.#api.print(
        {
          deviceId: printer.id,
          storage: result.storage,
          source: result.path,
          name: result.path.split('/').at(-1) ?? result.path,
        },
        this.#signal,
        guard,
      );
      this.#live();
      this.#notice = {
        key: 'slicer.plate.notice.printing',
        params: { printer: printer.name },
      };
      return true;
    });
    return ok === true;
  }

  /** Slices the plate if it changed, then adds the finished job to Print queue. */
  async enqueue(
    input: PlateInput,
    printer: { id: string; name: string },
    current: () => SendSnapshot,
  ): Promise<void> {
    const pressed = current();
    await this.#run(async () => {
      this.#step = 'queueing';
      const job = await this.#slicedJob(input);
      this.#live();
      if (!stillSendable(pressed, current())) throw new PlateChanged();
      this.#step = 'queueing';
      try {
        const { waiting } = await this.#api.enqueue(
          job.id,
          printer.id,
          this.#signal,
        );
        this.#live();
        this.#waiting = { ...this.#waiting, [printer.id]: waiting };
        this.#notice = {
          key: 'slicer.plate.notice.queued',
          params: { printer: printer.name },
        };
      } catch (error) {
        const block = queueBlockOf(describeFault(error).code);
        if (
          block !== undefined &&
          !canRecheckQueue(block) &&
          this.#queue?.printer === printer.id
        )
          this.#queue = { printer: printer.id, block };
        throw error;
      }
    });
  }

  /** Slices the plate if it changed and answers its G-code as a file. */
  async download(input: PlateInput): Promise<File | undefined> {
    return this.#run(async () => {
      const job = await this.#slicedJob(input);
      const result = job.result;
      if (!result) throw new Error('The job wrote no G-code');
      const blob = await this.#api.download(
        result.storage,
        result.path,
        this.#signal,
      );
      return new File([blob], result.path.split('/').at(-1) ?? 'plate.gcode');
    });
  }

  /** Stops the step once the workspace has closed, before anything else is sent. */
  #live(): void {
    if (this.#signal.aborted) throw new PageClosed();
  }

  async #run<T>(work: () => Promise<T>): Promise<T | undefined> {
    if (this.busy) return undefined;
    this.#fault = undefined;
    this.#notice = undefined;
    try {
      return await work();
    } catch (thrown) {
      if (this.#signal.aborted) return undefined;
      if (thrown instanceof PlateChanged)
        this.#notice = { key: 'slicer.plate.notice.changed', params: {} };
      else this.#fault = describeFault(thrown);
      return undefined;
    } finally {
      if (!this.#signal.aborted) this.#step = 'idle';
    }
  }

  async #savedPath(input: PlateInput): Promise<string> {
    const saved = this.#saved;
    if (saved?.storage === input.storage && saved.meshKey === input.meshKey)
      return saved.path;

    this.#live();
    this.#step = 'saving';
    const path = await this.#api.save(
      input.storage,
      placedName(input.name),
      makeSTL(input.meshes),
      this.#signal,
    );
    this.#live();
    this.#saved = { meshKey: input.meshKey, storage: input.storage, path };
    return path;
  }

  async #slicedJob(input: PlateInput): Promise<Job> {
    const signature = signatureOf(input);
    const known = this.slicedFor(signature);
    if (known) return known;

    const path = await this.#savedPath(input);
    this.#live();
    this.#step = 'slicing';
    let job = await this.#api.submit(
      {
        storage: input.storage,
        path,
        profile: input.profile,
        settings: input.settings,
      },
      this.#signal,
    );
    this.#live();
    while (!ENDED.has(job.state)) {
      await pause(this.#pollMs, this.#signal);
      job = await this.#api.job(job.id, this.#signal);
      this.#live();
    }
    if (job.state !== 'done') {
      // describeFault reads the code and the server's message off what is thrown.
      throw Object.assign(
        new Error(job.error?.message ?? `The slicing job ended ${job.state}`),
        { code: job.error?.code },
      );
    }
    this.#sliced = { signature, job };
    return job;
  }
}
