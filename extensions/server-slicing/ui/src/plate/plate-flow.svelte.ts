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
  type PrepareApi,
} from '../views/prepare-panel/prepare-api';
import type { PrintSettings } from './print-settings';
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
export type FlowStep = 'idle' | 'saving' | 'slicing' | 'sending';

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

/**
 * What each step of one action waits under: the guard it calls before every
 * request and after every wait, and the signal every request is sent with.
 */
interface Send {
  readonly guard: SendGuard;
  readonly signal: AbortSignal;
}

export class PlateFlow {
  #step = $state<FlowStep>('idle');
  #fault = $state.raw<FaultText | undefined>(undefined);
  #notice = $state.raw<FlowNotice | undefined>(undefined);
  #sliced = $state.raw<{ signature: string; job: Job } | undefined>(undefined);
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
      const path = await this.#savedPath(input, this.#pageOnly);
      this.#notice = { key: 'slicer.plate.notice.saved', params: { path } };
      return path;
    });
  }

  /** Slices the plate, or answers the job of the plate as it is. */
  async slice(input: PlateInput): Promise<Job | undefined> {
    return this.#run(async () => {
      const job = await this.#slicedJob(input, this.#pageOnly);
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
    // Aborted the moment the plate stops being sendable. Every request of the
    // print is sent with it, so one in flight is cut short and the SDK does
    // not send it again, which its own retries would otherwise do.
    const stale = new AbortController();
    const send: Send = {
      signal: AbortSignal.any([this.#signal, stale.signal]),
      guard: () => {
        this.#live();
        if (stale.signal.aborted || !stillSendable(pressed, current())) {
          stale.abort();
          throw new PlateChanged();
        }
      },
    };
    const watch = $effect.root(() => {
      $effect(() => {
        if (!stillSendable(pressed, current())) stale.abort();
      });
    });
    try {
      const ok = await this.#run(async () => {
        try {
          const job = await this.#slicedJob(input, send);
          const result = job.result;
          if (!result) throw new Error('The job wrote no G-code');
          send.guard();
          this.#step = 'sending';
          await this.#api.print(
            {
              deviceId: printer.id,
              storage: result.storage,
              source: result.path,
              name: result.path.split('/').at(-1) ?? result.path,
            },
            send.signal,
            send.guard,
          );
        } catch (thrown) {
          // A request cut short by the stale signal fails as a network
          // error; it is the change the person should be told about.
          if (stale.signal.aborted && !this.#signal.aborted)
            throw new PlateChanged();
          throw thrown;
        }
        this.#live();
        this.#notice = {
          key: 'slicer.plate.notice.printing',
          params: { printer: printer.name },
        };
        return true;
      });
      return ok === true;
    } finally {
      watch();
    }
  }

  /** Slices the plate if it changed and answers its G-code as a file. */
  async download(input: PlateInput): Promise<File | undefined> {
    return this.#run(async () => {
      const job = await this.#slicedJob(input, this.#pageOnly);
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

  /** What an action that sends nothing to a printer waits under: the page alone. */
  get #pageOnly(): Send {
    return {
      guard: () => {
        this.#live();
      },
      signal: this.#signal,
    };
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

  async #savedPath(input: PlateInput, send: Send): Promise<string> {
    const saved = this.#saved;
    if (saved?.storage === input.storage && saved.meshKey === input.meshKey)
      return saved.path;

    send.guard();
    this.#step = 'saving';
    const path = await this.#api.save(
      input.storage,
      placedName(input.name),
      makeSTL(input.meshes),
      send.signal,
    );
    this.#saved = { meshKey: input.meshKey, storage: input.storage, path };
    send.guard();
    return path;
  }

  async #slicedJob(input: PlateInput, send: Send): Promise<Job> {
    const signature = signatureOf(input);
    const known = this.slicedFor(signature);
    if (known) return known;

    const path = await this.#savedPath(input, send);
    send.guard();
    this.#step = 'slicing';
    let job = await this.#api.submit(
      {
        storage: input.storage,
        path,
        profile: input.profile,
        settings: input.settings,
      },
      send.signal,
    );
    send.guard();
    while (!ENDED.has(job.state)) {
      await pause(this.#pollMs, send.signal);
      send.guard();
      job = await this.#api.job(job.id, send.signal);
      send.guard();
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
