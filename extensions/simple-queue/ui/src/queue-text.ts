// What the views say about a queue, as plain functions over the answers of
// the routes, so a test reads them without a browser.
import {
  isPrintFile,
  type PrinterState,
  printerStateOf,
} from '@prusa/connect-ui/domain';
import type { BadgeVariant } from '@prusa/connect-ui/primitives';

import type { Entry, Printer, Queue, Translate } from './api';

/** The suffixes a printer prints, for the file dialog of the browser. */
export const PRINTABLE = ['.bgcode', '.gcode', '.gco', '.g'];

/** Whether the add route takes a file of this name. */
export const isPrintable = isPrintFile;

/** What the printer is doing, with a printer that is away read as OFFLINE. */
export const printerState = (printer: Printer): PrinterState =>
  printer.connected ? printerStateOf(printer.state) : 'OFFLINE';

/** The badge an ended print is drawn in. */
export const outcomeBadge = (outcome: Entry['outcome']): BadgeVariant => {
  switch (outcome) {
    case 'finished':
      return 'stateFinished';
    case 'failed':
      return 'stateFault';
    case 'cancelled':
      return 'stateQuiet';
    case 'unknown':
    case undefined:
      return 'outline';
  }
};

/** The file on its way or printing, if there is one. */
export const activeOf = (queue: Queue): Entry | undefined =>
  queue.entries.find(
    (entry) => entry.state === 'sending' || entry.state === 'printing',
  );

/**
 * What the active entry is doing, as the key of its badge. A file the printer
 * already holds is not sent anywhere: it is only being started.
 */
export const activeKey = (entry: Entry): 'printing' | 'sending' | 'opening' => {
  if (entry.state === 'printing') return 'printing';

  return entry.storage === '' ? 'opening' : 'sending';
};

/** The files that wait, in the order they print. */
export const waitingOf = (queue: Queue): Entry[] =>
  queue.entries.filter((entry) => entry.state === 'waiting');

/** What the Queue tab offers a person who may change the queue. */
export interface Offers {
  /**
   * The start button: the person says the bed is clear and the next file
   * starts now, as them. A printer with a ready state names it Set ready and
   * print, any other printer Print.
   */
  printNext: boolean;
  /**
   * Set ready, with nothing waiting: the next file added then starts at
   * once. With a file waiting the start button says and does both.
   */
  markReady: boolean;
  /** Unset ready: take the mark back. */
  unmarkReady: boolean;
  /**
   * The printer is marked ready already. Set ready and the start button are
   * then drawn disabled, and Unset ready is live; otherwise the other way
   * round, so a person always sees both and which one applies.
   */
  readyNow: boolean;
  /**
   * The switch that lets the queue print when the printer is marked ready on
   * its own screen. It is shown on every printer that can be ready, on or
   * off, so it can be taken back.
   */
  allowAlone: boolean;
}

export const offersOf = (queue: Queue): Offers => {
  const none = {
    printNext: false,
    markReady: false,
    unmarkReady: false,
    readyNow: false,
    allowAlone: false,
  };
  if (!queue.may_change) return none;

  const ready = queue.printer.can_be_ready;
  // The switch is a setting and not an action on the printer, so it is there
  // for a printer that is away too, and can be taken back then.
  if (!queue.printer.connected) return { ...none, allowAlone: ready };

  const rests = activeOf(queue) === undefined && queue.printer.free;
  const waiting = waitingOf(queue).length > 0;

  return {
    printNext: rests && waiting,
    markReady: rests && ready && !waiting,
    unmarkReady: rests && ready,
    readyNow: ready && queue.printer.ready,
    allowAlone: ready,
  };
};

/** Which sentence the status card says about what happens next. */
export type Outlook =
  | 'away'
  | 'sending'
  | 'opening'
  | 'printing'
  | 'empty'
  | 'emptyReady'
  | 'starting'
  | 'markBed'
  | 'pressPrint'
  | 'byHand'
  | 'printerBusy'
  | 'held';

export const outlookOf = (queue: Queue): Outlook => {
  const active = activeOf(queue);
  if (active?.state === 'sending') return activeKey(active);
  if (active?.state === 'printing') return 'printing';
  if (!queue.printer.connected) return 'away';

  const next = waitingOf(queue)[0];
  const ready = queue.printer.can_be_ready && queue.printer.ready;
  if (next === undefined) return ready ? 'emptyReady' : 'empty';

  if (!queue.printer.free) return 'printerBusy';
  // Every note holds the file, so nothing starts it in a moment.
  if ((next.note ?? '') !== '') return 'held';
  // A printer that never says its bed is clear cannot be ready, so nothing
  // starts by itself on it, whatever the queue is allowed.
  if (!queue.printer.can_be_ready) return 'byHand';
  if (!queue.starts_alone) return 'pressPrint';

  return ready ? 'starting' : 'markBed';
};

/** A principal such as user:alice as a person reads it, and "you" for the reader. */
export const personOf = (
  principal: string | undefined,
  me: string | undefined,
): { you: boolean; name: string } => {
  if (!principal) return { you: false, name: '' };
  if (me !== undefined && principal === me) return { you: true, name: '' };
  const colon = principal.indexOf(':');

  return {
    you: false,
    name: colon < 0 ? principal : principal.slice(colon + 1),
  };
};

/**
 * The folder a file is in, as storage and path, without its own name. A file
 * on the printer names no storage, and its path starts with the printer's
 * drive, such as usb.
 */
export const folderOf = (entry: Pick<Entry, 'storage' | 'path'>): string => {
  const slash = entry.path.lastIndexOf('/');
  const folder = slash < 0 ? '' : entry.path.slice(0, slash);
  if (entry.storage === '') return folder;

  return folder === '' ? entry.storage : `${entry.storage}/${folder}`;
};

/**
 * Where a file is, in words: its folder on the printer, such as usb, or its
 * storage and folder on this server.
 */
export const whereOf = (
  entry: Pick<Entry, 'storage' | 'path'>,
  t: Translate,
): string =>
  t(
    entry.storage === ''
      ? 'simple-queue.entry.location'
      : 'simple-queue.entry.locationStorage',
    { folder: folderOf(entry) },
  );

/** How many files a printer's queue holds: the one on its way and those that wait. */
export const queuedOf = (queue: Queue): number =>
  waitingOf(queue).length + (activeOf(queue) ? 1 : 0);
