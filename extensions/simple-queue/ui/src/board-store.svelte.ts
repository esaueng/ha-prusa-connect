// What the Print queue page shows of every printer at once: each queue read
// in one request every few seconds, however many printers there are. A file
// is moved within its queue here too; the rest is changed on the printer's
// Queue tab.
import { describeFault, type FaultText } from '@prusa/connect-ui/i18n';

import type { Queue, QueueApi } from './api';
import { followQueues, type QueueEvents } from './following';
import { poll } from './polling';
import { keptLoad, type Load } from './printer-store.svelte';

/** How long events wait before they read every queue again. */
export const WAKE_MS = 5_000;

/** What the page draws, and what a story hands it in place of the server. */
export interface BoardView {
  readonly load: Load;
  readonly fault: FaultText | undefined;
  /** Every queue the person sees, in the server's order. */
  readonly queues: readonly Queue[];
  /** Why the last move failed, until the next one succeeds. */
  readonly actionFault?: FaultText | undefined;
  /** Moves a waiting file; position counts the waiting files from 0. */
  move?(device: string, entry: string, position: number): Promise<void>;
}

export class BoardStore implements BoardView {
  load = $state<Load>('loading');
  fault = $state<FaultText | undefined>(undefined);
  queues = $state.raw<readonly Queue[]>([]);
  actionFault = $state<FaultText | undefined>(undefined);

  readonly #api: QueueApi;
  #controller = new AbortController();
  #latest = 0;
  #woken: ReturnType<typeof setTimeout> | undefined;
  // Reads every queue now: once the timer runs, through it, so a burst of
  // events costs one read.
  #wake: () => void = () => void this.refresh();

  constructor(api: QueueApi) {
    this.#api = api;
  }

  /** Reads every queue, and again a while after each read, until dispose. */
  start(): void {
    this.#wake = poll(() => this.refresh(), this.#controller.signal);
  }

  /**
   * Reads every queue again when the server says something changed on a
   * printer the page shows, at most once each WAKE_MS: a fleet whose
   * printers change all the time costs one read each few seconds. An event
   * of no known printer, such as a lost stream, reads at once.
   */
  follow(events: QueueEvents): void {
    followQueues(events, this.#controller.signal, (device) => {
      if (device === undefined) {
        this.#wake();

        return;
      }
      if (!this.queues.some((queue) => queue.device_id === device)) return;
      if (this.#woken !== undefined) return;
      this.#woken = setTimeout(() => {
        this.#woken = undefined;
        this.#wake();
      }, WAKE_MS);
    });
  }

  /** Stops the reading and every request still open. */
  dispose(): void {
    this.#controller.abort();
    clearTimeout(this.#woken);
  }

  async move(device: string, entry: string, position: number): Promise<void> {
    const signal = this.#controller.signal;
    try {
      await this.#api.moveEntry(
        { path: { device, entry }, body: { position } },
        signal,
      );
      this.actionFault = undefined;
    } catch (thrown) {
      if (signal.aborted) return;
      this.actionFault = describeFault(thrown);
    }
    await this.refresh();
  }

  async refresh(): Promise<void> {
    const signal = this.#controller.signal;
    // Only the newest answer is drawn: a read that began before a move
    // answered, or before another read, would draw an older order.
    const ticket = (this.#latest += 1);
    try {
      const read = (await this.#api.readBoard({}, signal)).queues;
      if (ticket !== this.#latest) return;
      this.queues = read;
      this.load = 'ready';
      this.fault = undefined;
    } catch (thrown) {
      if (signal.aborted || ticket !== this.#latest) return;
      this.load = keptLoad(this.load, thrown);
      this.fault = describeFault(thrown);
    }
  }
}
