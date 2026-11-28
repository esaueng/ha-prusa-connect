import {
  PRINTER_STATE_TONE,
  type PrinterState,
  type PrinterStateTone,
} from '@prusa/connect-ui/domain';
import type { BadgeVariant } from '@prusa/connect-ui/primitives';

// The badge each state wears on the printer page, by the tint the domain gives it.
const BADGE: Record<PrinterStateTone, BadgeVariant> = {
  active: 'stateActive',
  busy: 'stateBusy',
  moving: 'stateMoving',
  ready: 'stateReady',
  finished: 'stateFinished',
  stopped: 'stateStopped',
  paused: 'statePaused',
  attention: 'stateAttention',
  fault: 'stateFault',
  idle: 'stateIdle',
  quiet: 'stateQuiet',
  offline: 'stateOffline',
};

export const stateBadge = (state: PrinterState): BadgeVariant =>
  BADGE[PRINTER_STATE_TONE[state]];
