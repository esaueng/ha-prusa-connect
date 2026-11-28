// Whether the G-code sliced for a plate may still go to the printer.
//
// The page takes a snapshot when Print or Add to queue is pressed. The flow
// checks it after slicing, and the service checks it after its lookups, before
// starting a print or adding an entry to the queue.

import type { PrinterState } from '@prusa/connect-ui/domain';

/** The plate and the printer as one send depends on them. */
export interface SendSnapshot {
  /** The plate's revision, profile, settings, storage and bed, as one text. */
  readonly plate: string;
  readonly printer: string | undefined;
  readonly state: PrinterState | undefined;
  /** What the Print button does with this printer now. */
  readonly action: 'print' | 'queue';
  /** The printer takes the plate now, and every part fits its bed. */
  readonly sendable: boolean;
  /**
   * A file is still being read onto the plate, so the plate on the screen is
   * not yet the one that was sliced. Absent means no file is being read.
   */
  readonly importing?: boolean;
}

/**
 * True when nothing the G-code depends on has changed since `pressed` and the
 * printer can still take it. A printer that started printing turns Print into
 * Add to queue, so it no longer matches a snapshot taken for Print.
 */
export const stillSendable = (
  pressed: SendSnapshot,
  now: SendSnapshot,
): boolean =>
  now.sendable &&
  now.importing !== true &&
  now.plate === pressed.plate &&
  now.printer === pressed.printer &&
  now.state === pressed.state &&
  now.action === pressed.action;

/** Thrown where a send stops because the plate or the printer changed. */
export class PlateChanged extends Error {
  override readonly name = 'PlateChanged';

  constructor() {
    super('The plate or the printer changed while the plate was sliced');
  }
}

/** Thrown where a send stops because the page closed. */
export class PageClosed extends Error {
  override readonly name = 'PageClosed';

  constructor() {
    super('The workspace closed');
  }
}

/**
 * What a service that sends the plate calls after each of its waits, right
 * before its next request. It throws when the page closed or the plate may no
 * longer be sent, so nothing further goes out.
 *
 * The service passes `'final'` right before the request that starts the print
 * or adds the queue entry. Once that call passes, the send is committed: that
 * request goes out without a signal that could cut it short, and its answer,
 * success or failure, is reported as the server gave it.
 */
export type SendGuard = (step?: 'final') => void;
