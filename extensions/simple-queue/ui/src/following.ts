// What the pages of the queue follow on the server's event stream, so they
// read a queue again when the server says something it shows changed.
import type { PrusaConnect } from '@prusa/connect';
import { deviceIdOf } from '@prusa/connect-ui/data';

/** The events that change what a queue shows. */
export const QUEUE_EVENTS = [
  'com.prusa.connect.device.state.changed',
  'com.prusa.connect.device.transfer.started',
  'com.prusa.connect.device.transfer.settled',
  'com.prusa.connect.device.files.changed',
  'com.prusa.connect.device.job.started',
  'com.prusa.connect.device.job.settled',
] as const;

/** The part of the host's client the queue reads events from. */
export type QueueEvents = Pick<PrusaConnect, 'events'>;

/**
 * Calls changed for each event of QUEUE_EVENTS until signal aborts, with the
 * device the event is about, or undefined when the stream lost its place and
 * anything may have changed meanwhile. A stream that ends only leaves the
 * page to its timer.
 */
export function followQueues(
  events: QueueEvents,
  signal: AbortSignal,
  changed: (device: string | undefined) => void,
): void {
  const subscription = events.events({ types: QUEUE_EVENTS, signal });
  void (async () => {
    try {
      for await (const event of subscription) {
        changed(deviceIdOf(event));
      }
    } catch {
      // The stream ended. The timer goes on reading.
    }
  })();
}
