import { describe, expect, it } from 'vitest';

import {
  charlie,
  henry,
  printer,
  xl,
} from '../views/prepare-panel/prepare-fixtures';
import {
  decidePrint,
  type PlateReadiness,
  printerChoiceOf,
  printerChoicesOf,
} from './printers';

const ready: PlateReadiness = {
  parts: 2,
  outside: 0,
  profile: true,
  slicer: true,
  storage: true,
  busy: false,
};

describe('a printer in the picker', () => {
  it('reads the state, the material, the progress and the bed', () => {
    expect(printerChoiceOf(charlie)).toEqual({
      id: 'charlie',
      name: 'Charlie',
      model: 'MK4S',
      state: 'PRINTING',
      stateKey: 'state.PRINTING',
      progress: 63,
      material: 'PETG',
      bed: { min_x: 0, min_y: 0, max_x: 250, max_y: 210, height: 220 },
      mayPrint: true,
    });
  });

  it('reads a printer that left as offline, whatever it said last', () => {
    expect(printerChoiceOf({ ...henry, connected: false }).state).toBe(
      'OFFLINE',
    );
  });

  it('has no bed when the driver reports none', () => {
    const noVolume = { ...henry };
    delete noVolume.build_volume;
    expect(printerChoiceOf(noVolume).bed).toBeUndefined();
  });

  it('lists the printers that can print now first, each group by name', () => {
    const zed = printer('zed', 'Zed', 'READY');
    const retired = printer('old', 'Old', 'IDLE', {
      retired: { at: '2026-01-01T00:00:00Z', reason: 'ownership_changed' },
    });
    expect(
      printerChoicesOf([charlie, zed, retired, xl, henry]).map((one) => one.id),
    ).toEqual(['henry', 'xl', 'zed', 'charlie']);
  });
});

describe('what the Print button does', () => {
  it('prints on an idle or ready printer', () => {
    expect(decidePrint(printerChoiceOf(henry), ready, false)).toEqual({
      action: 'print',
      block: undefined,
    });
    expect(
      decidePrint(printerChoiceOf(printer('r', 'R', 'READY')), ready, false),
    ).toEqual({ action: 'print', block: undefined });
  });

  it('queues on a printer that prints or holds a finished print', () => {
    for (const state of ['PRINTING', 'PAUSED', 'FINISHED', 'STOPPED', 'BUSY']) {
      const busy = printerChoiceOf(printer('b', 'B', state));
      expect(decidePrint(busy, ready, true)).toEqual({
        action: 'queue',
        block: undefined,
      });
      // This module reaches no print queue, so it says why.
      expect(decidePrint(busy, ready, false)).toEqual({
        action: 'queue',
        block: 'noQueue',
      });
    }
  });

  it('refuses a printer that is away, in error or not the person’s to use', () => {
    expect(
      decidePrint(printerChoiceOf({ ...henry, connected: false }), ready, true)
        .block,
    ).toBe('offline');
    expect(
      decidePrint(printerChoiceOf(printer('e', 'E', 'ERROR')), ready, true)
        .block,
    ).toBe('state');
    expect(
      decidePrint(printerChoiceOf({ ...henry, verbs: [] }), ready, true).block,
    ).toBe('notAllowed');
    expect(decidePrint(undefined, ready, true).block).toBe('noPrinter');
  });

  it('refuses a plate that cannot be sliced before it asks the printer', () => {
    const idle = printerChoiceOf(henry);
    expect(decidePrint(idle, { ...ready, parts: 0 }, false).block).toBe(
      'empty',
    );
    expect(decidePrint(idle, { ...ready, outside: 1 }, false).block).toBe(
      'outside',
    );
    expect(decidePrint(idle, { ...ready, profile: false }, false).block).toBe(
      'noProfile',
    );
    expect(decidePrint(idle, { ...ready, slicer: false }, false).block).toBe(
      'noSlicer',
    );
    expect(decidePrint(idle, { ...ready, storage: false }, false).block).toBe(
      'noStorage',
    );
    expect(decidePrint(idle, { ...ready, busy: true }, false).block).toBe(
      'busy',
    );
    // A plate outside the bed is the first thing said, even on a busy printer.
    expect(
      decidePrint(printerChoiceOf(charlie), { ...ready, outside: 1 }, false),
    ).toEqual({ action: 'queue', block: 'outside' });
  });
});
