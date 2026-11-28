// The printers the plate can go to, as the host's fleet lists them, and what
// the Print button does for the one chosen.
import {
  activeMaterialOf,
  type Device,
  PRINTER_STATE_LABEL_KEY,
  type PrinterState,
  printerStateOf,
  STATE_PATH,
  textAt,
} from '@prusa/connect-ui/domain';

import { START_PRINT_VERB } from '@prusa/connect-ui/data';

import type { STLBed } from '../api';

/** One printer, as the picker draws it. */
export interface PrinterChoice {
  readonly id: string;
  readonly name: string;
  readonly model: string;
  readonly state: PrinterState;
  /** The catalogue key of the state's word. */
  readonly stateKey: string;
  /** How far the print on it is, in whole percent, while one runs. */
  readonly progress: number | undefined;
  /** The material loaded in the tool printing now, such as PETG. */
  readonly material: string | undefined;
  /** The print volume the driver reports, from 0 in X and Y. */
  readonly bed: STLBed | undefined;
  /** The printer takes prints and this person may start one. */
  readonly mayPrint: boolean;
}

/** The fleet's rows carry no live telemetry, so material is read from the snapshot. */
const SNAPSHOT_ONLY = { value: (): undefined => undefined };

export const printerChoiceOf = (device: Device): PrinterChoice => {
  const state = device.connected
    ? printerStateOf(textAt(device, STATE_PATH) ?? 'UNKNOWN')
    : 'OFFLINE';
  const volume = device.build_volume;
  return {
    id: device.id,
    name: device.name,
    model: device.model,
    state,
    stateKey: PRINTER_STATE_LABEL_KEY[state],
    progress:
      device.job?.running === true
        ? Math.round(device.job.progress)
        : undefined,
    material: activeMaterialOf(SNAPSHOT_ONLY, device),
    bed:
      volume && volume.x_mm > 0 && volume.y_mm > 0 && volume.z_mm > 0
        ? {
            min_x: 0,
            min_y: 0,
            max_x: volume.x_mm,
            max_y: volume.y_mm,
            height: volume.z_mm,
          }
        : undefined,
    mayPrint:
      (device.capabilities ?? []).includes(START_PRINT_VERB) &&
      (device.verbs ?? []).includes(START_PRINT_VERB),
  };
};

/** The printers, the ones that can print now first, each group by name. */
export const printerChoicesOf = (devices: readonly Device[]): PrinterChoice[] =>
  devices
    .filter((device) => device.retired === undefined)
    .map(printerChoiceOf)
    .sort(
      (a, b) =>
        Number(FREE.has(b.state)) - Number(FREE.has(a.state)) ||
        a.name.localeCompare(b.name),
    );

/**
 * The states a print starts from. FINISHED and STOPPED leave the last print on
 * the bed, so the core's own print picker does not start one there, and
 * neither does this page.
 */
const FREE: ReadonlySet<PrinterState> = new Set(['IDLE', 'READY']);

/** The states of a printer that is there and will be free later. */
const LATER: ReadonlySet<PrinterState> = new Set([
  'PRINTING',
  'BUSY',
  'PAUSED',
  'MANIPULATING',
  'FINISHED',
  'STOPPED',
  'ATTENTION',
]);

/** Why the Print button cannot run, as a key under `slicer.plate.blocked`. */
export type PrintBlock =
  | 'empty'
  | 'outside'
  | 'noProfile'
  | 'noSlicer'
  | 'noStorage'
  | 'busy'
  | 'noPrinter'
  | 'offline'
  | 'notAllowed'
  | 'state'
  | QueueBlock;

export type QueueBlock =
  | 'queueChecking'
  | 'queueInstall'
  | 'queueOffline'
  | 'queueApproval'
  | 'queueDenied'
  | 'queuePrinterMissing'
  | 'queuePrinterUnknown'
  | 'queueUnavailable';

export const canRecheckQueue = (block: PrintBlock | undefined): boolean =>
  block === 'queueUnavailable' ||
  block === 'queuePrinterUnknown' ||
  block === 'queueOffline';

export interface PrintDecision {
  /** Print starts the file now; queue adds it to the printer's queue. */
  readonly action: 'print' | 'queue';
  /** Undefined when the button runs. */
  readonly block: PrintBlock | undefined;
}

export interface PlateReadiness {
  readonly parts: number;
  readonly outside: number;
  readonly profile: boolean;
  readonly slicer: boolean;
  readonly storage: boolean;
  /** Something is being saved, sliced or sent. */
  readonly busy: boolean;
}

/**
 * What the Print button does. An idle printer prints the plate now. A printer
 * that is printing, or holds a finished print on its bed, takes it into its
 * queue. Neither runs while the plate cannot be sliced.
 *
 * The queue check supplies a reason while the selected printer cannot queue.
 */
export const decidePrint = (
  printer: PrinterChoice | undefined,
  plate: PlateReadiness,
  queueBlock: QueueBlock | undefined,
): PrintDecision => {
  const action =
    printer && LATER.has(printer.state)
      ? ('queue' as const)
      : ('print' as const);
  const block = plateBlockOf(plate) ?? printerBlockOf(printer, queueBlock);
  return { action, block };
};

/** Why the plate itself cannot be sliced, whatever the printer does. */
export const plateBlockOf = (plate: PlateReadiness): PrintBlock | undefined => {
  if (plate.busy) return 'busy';
  if (plate.parts === 0) return 'empty';
  if (plate.outside > 0) return 'outside';
  if (!plate.profile) return 'noProfile';
  if (!plate.slicer) return 'noSlicer';
  if (!plate.storage) return 'noStorage';
  return undefined;
};

/** Why the printer cannot take the plate now, whatever the plate is. */
export const printerBlockOf = (
  printer: PrinterChoice | undefined,
  queueBlock: QueueBlock | undefined,
): PrintBlock | undefined => {
  if (!printer) return 'noPrinter';
  if (printer.state === 'OFFLINE') return 'offline';
  if (!printer.mayPrint) return 'notAllowed';
  if (FREE.has(printer.state)) return undefined;
  if (LATER.has(printer.state)) return queueBlock;
  return 'state';
};
