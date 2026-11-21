// The queues of every printer the person sees, in one read. The dashboard
// draws a card for each printer and each card shows a line of this module, so
// the cards share this store: it reads the server while at least one card or
// the page is on screen, and stops when the last one goes.
import { describeFault, type FaultText } from '@prusa/connect-ui/i18n';

import type { Overview, QueueApi, Summary } from './api';
import { followQueues, type QueueEvents } from './following';
import { poll } from './polling';
import type { Load } from './printer-store.svelte';

/** What the page and the card lines draw. */
export interface OverviewView {
  readonly load: Load;
  readonly fault: FaultText | undefined;
  readonly overview: Overview | undefined;
}

const refusedStatus = 403;

const statusOf = (thrown: unknown): number | undefined =>
  typeof thrown === 'object' && thrown !== null && 'status' in thrown
    ? Number(thrown.status)
    : undefined;

export class OverviewStore implements OverviewView {
  load = $state<Load>('loading');
  fault = $state<FaultText | undefined>(undefined);
  overview = $state.raw<Overview | undefined>(undefined);

  readonly #api: Pick<QueueApi, 'readSummary'>;
  #watchers = 0;
  #controller: AbortController | undefined;

  readonly #events: QueueEvents | undefined;

  /** events, when given, wakes a read when a printer's queue may have changed. */
  constructor(api: Pick<QueueApi, 'readSummary'>, events?: QueueEvents) {
    this.#api = api;
    this.#events = events;
  }

  /** The line of one printer, once the overview has been read. */
  of(device: string): Summary | undefined {
    return this.overview?.printers.find((row) => row.device_id === device);
  }

  /** Starts reading for one more watcher. The answer lets it go. */
  watch(): () => void {
    this.#watchers += 1;
    if (this.#watchers === 1) this.#start();
    let done = false;

    return () => {
      if (done) return;
      done = true;
      this.#watchers -= 1;
      if (this.#watchers === 0) this.#stop();
    };
  }

  /** Stops every read, whoever still watches. */
  dispose(): void {
    this.#watchers = 0;
    this.#stop();
  }

  async refresh(): Promise<void> {
    const signal = this.#controller?.signal;
    if (!signal) return;
    try {
      this.overview = await this.#api.readSummary({}, signal);
      this.load = 'ready';
      this.fault = undefined;
    } catch (thrown) {
      if (signal.aborted) return;
      this.load = statusOf(thrown) === refusedStatus ? 'refused' : 'failed';
      this.fault = describeFault(thrown);
    }
  }

  #start(): void {
    const controller = new AbortController();
    this.#controller = controller;
    const wake = poll(() => this.refresh(), controller.signal);
    if (this.#events) followQueues(this.#events, controller.signal, wake);
  }

  #stop(): void {
    this.#controller?.abort();
    this.#controller = undefined;
  }
}
