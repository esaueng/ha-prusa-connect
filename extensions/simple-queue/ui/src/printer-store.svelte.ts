// What the Queue tab of one printer shows, read from the module's routes. It
// reads the queue again every few seconds while the tab is open, because a
// print moves on at the printer without telling the page.
import { describeFault, type FaultText } from '@prusa/connect-ui/i18n';

import { bodyOf, type FileAt } from './adding';
import {
  type Entry,
  type Queue,
  type QueueApi,
  VERB_PAUSE,
  VERB_RESUME,
  VERB_STOP,
} from './api';
import type { Grants } from './grants';
import { followQueues, type QueueEvents } from './following';
import { poll } from './polling';

/** How a read of the server ended. */
export type Load = 'loading' | 'ready' | 'failed' | 'refused';

/** How putting several files on the queue ended. */
export interface AddedFiles {
  added: number;
  /** Each file that was refused, by its name, with the server's reason. */
  failed: { name: string; fault: FaultText }[];
}

/** What the tab draws, and what a story hands it in place of the server. */
export interface PrinterQueueView {
  readonly load: Load;
  readonly fault: FaultText | undefined;
  readonly queue: Queue | undefined;
  /** The prints that ended, newest first, read on their own route. */
  readonly history: readonly Entry[];
  /** An action that failed, shown above the queue until the next one. */
  readonly actionFault: FaultText | undefined;
  /** An action is on its way, so its buttons wait for it. */
  readonly busy: boolean;
  printNext(): Promise<void>;
  /** True says the bed is clear, false takes that back. */
  setReady(ready: boolean): Promise<void>;
  remove(entry: string): Promise<void>;
  /** position counts the waiting files from 0, the next one. */
  move(entry: string, position: number): Promise<void>;
  clearHistory(): Promise<void>;
  /**
   * Lets the module start the next file by itself once the printer is marked
   * ready, or takes that back.
   */
  setAlone(allowed: boolean): Promise<void>;
  /** Pauses the print from the queue, resumes it, or stops it for good. */
  pause(): Promise<void>;
  resume(): Promise<void>;
  stop(): Promise<void>;
  /** Whether the person may switch that. False greys the switch out. */
  readonly mayAllow: boolean;
  /** Puts the files on the queue one after another, in this order. */
  addFiles(files: readonly FileAt[]): Promise<AddedFiles>;
}

const refusedStatus = 403;

/** Whether a failed read was a refusal or a failure. */
export const loadOf = (thrown: unknown): Load =>
  typeof thrown === 'object' &&
  thrown !== null &&
  'status' in thrown &&
  Number(thrown.status) === refusedStatus
    ? 'refused'
    : 'failed';

/**
 * What a page shows after a read failed. A queue already on screen stays
 * there with the fault above it, so one failed read does not hide it; only a
 * refusal, or a failure before anything was read, replaces it.
 */
export const keptLoad = (held: Load, thrown: unknown): Load => {
  const now = loadOf(thrown);

  return held === 'ready' && now === 'failed' ? 'ready' : now;
};

/**
 * The actions on one printer's queue. A store that reads the queue on its own
 * and a lane of the page that reads every queue at once both act through it.
 */
export abstract class QueueActions {
  actionFault = $state<FaultText | undefined>(undefined);
  busy = $state(false);
  // True until the server says otherwise, so a read that failed leaves the
  // switch live and the core's own refusal is what the person sees.
  mayAllow = $state(true);

  protected readonly api: QueueApi;
  readonly #grants: Grants;
  protected readonly device: string;

  constructor(api: QueueApi, grants: Grants, device: string) {
    this.api = api;
    this.#grants = grants;
    this.device = device;
  }

  /** Stops with every request still open when it aborts. */
  protected abstract get signal(): AbortSignal;
  /** Draws a queue a route answered. */
  protected abstract accept(queue: Queue): void;
  abstract refresh(): Promise<void>;

  // An answer that carries the queue is drawn at once. The rest read it again.
  async #act(action: () => Promise<unknown>): Promise<void> {
    this.busy = true;
    try {
      const answer = await action();
      if (isQueue(answer)) this.accept(answer);
      this.actionFault = undefined;
    } catch (thrown) {
      if (this.signal.aborted) return;
      this.actionFault = describeFault(thrown);
    } finally {
      this.busy = false;
    }
    await this.refresh();
  }

  #at() {
    return { path: { device: this.device } };
  }

  printNext(): Promise<void> {
    return this.#act(() => this.api.startNext(this.#at(), this.signal));
  }

  setReady(ready: boolean): Promise<void> {
    return this.#act(() =>
      this.api.setReady({ ...this.#at(), body: { ready } }, this.signal),
    );
  }

  remove(entry: string): Promise<void> {
    return this.#act(() =>
      this.api.removeEntry(
        { path: { device: this.device, entry } },
        this.signal,
      ),
    );
  }

  move(entry: string, position: number): Promise<void> {
    return this.#act(() =>
      this.api.moveEntry(
        { path: { device: this.device, entry }, body: { position } },
        this.signal,
      ),
    );
  }

  clearHistory(): Promise<void> {
    return this.#act(() => this.api.clearHistory(this.#at(), this.signal));
  }

  pause(): Promise<void> {
    return this.#act(() => this.api.command(this.device, VERB_PAUSE));
  }

  resume(): Promise<void> {
    return this.#act(() => this.api.command(this.device, VERB_RESUME));
  }

  stop(): Promise<void> {
    return this.#act(() => this.api.command(this.device, VERB_STOP));
  }

  setAlone(allowed: boolean): Promise<void> {
    return this.#act(() =>
      allowed
        ? this.#grants.allow(this.device)
        : this.#grants.disallow(this.device),
    );
  }

  /** Asks once whether the person may switch what setAlone switches. */
  protected async readMayAllow(): Promise<void> {
    try {
      this.mayAllow = await this.#grants.may(this.device);
    } catch {
      // The switch stays live, and the core answers the press.
    }
  }

  // One request per file, in order, so the files wait in the order they were
  // picked. A refused file does not stop the ones after it.
  async addFiles(files: readonly FileAt[]): Promise<AddedFiles> {
    const signal = this.signal;
    const done: AddedFiles = { added: 0, failed: [] };
    this.busy = true;
    try {
      for (const file of files) {
        try {
          this.accept(
            await this.api.addEntry(
              { ...this.#at(), body: bodyOf(file) },
              signal,
            ),
          );
          done.added += 1;
        } catch (thrown) {
          if (signal.aborted) return done;
          done.failed.push({
            name: file.path.split('/').at(-1) ?? file.path,
            fault: describeFault(thrown),
          });
        }
      }
    } finally {
      this.busy = false;
    }
    await this.refresh();

    return done;
  }
}

export type { QueueEvents } from './following';

export class PrinterQueueStore
  extends QueueActions
  implements PrinterQueueView
{
  load = $state<Load>('loading');
  fault = $state<FaultText | undefined>(undefined);
  queue = $state.raw<Queue | undefined>(undefined);
  history = $state.raw<readonly Entry[]>([]);

  #controller = new AbortController();
  // The number of the newest read or action answer, so an older one is not drawn.
  #latest = 0;

  protected get signal(): AbortSignal {
    return this.#controller.signal;
  }

  protected accept(queue: Queue): void {
    this.#latest += 1;
    this.queue = queue;
  }

  // Reads the queue now: once the timer runs, through it, so a burst of
  // events costs one read.
  #wake: () => void = () => void this.refresh();

  /** Reads the queue, and again a while after each read, until dispose. */
  start(): void {
    void this.readMayAllow();
    this.#wake = poll(() => this.refresh(), this.#controller.signal);
  }

  /**
   * Reads the queue again at once when the server says something changed on
   * this printer that the queue shows: its state changed, a file set off,
   * landed or was not sent, its files changed, a print started or ended.
   * The timer still reads it, so a stream that is gone only makes the queue
   * slower to follow.
   */
  follow(events: QueueEvents): void {
    followQueues(events, this.#controller.signal, (device) => {
      if (device === undefined || device === this.device) this.#wake();
    });
  }

  /** Stops the reading and every request still open. */
  dispose(): void {
    this.#controller.abort();
  }

  async refresh(): Promise<void> {
    const signal = this.#controller.signal;
    const at = { path: { device: this.device } };
    // Only the newest answer is drawn: a read that began before an action
    // answered, or before another read, would draw an older queue.
    const ticket = (this.#latest += 1);
    try {
      const [queue, ended] = await Promise.all([
        this.api.readQueue(at, signal),
        this.api.readHistory(at, signal),
      ]);
      if (ticket !== this.#latest) return;
      this.queue = queue;
      this.history = ended.history;
      this.load = 'ready';
      this.fault = undefined;
    } catch (thrown) {
      if (signal.aborted || ticket !== this.#latest) return;
      this.load = keptLoad(this.load, thrown);
      this.fault = describeFault(thrown);
    }
  }
}

const isQueue = (value: unknown): value is Queue =>
  typeof value === 'object' &&
  value !== null &&
  'entries' in value &&
  'device_id' in value;
