import { describe, expect, it } from 'vitest';

import type { Queue } from './api';
import {
  folderOf,
  isPrintable,
  offersOf,
  outlookOf,
  personOf,
  waitingOf,
} from './queue-text';
import {
  awayQueue,
  emptyQueue,
  heldQueue,
  manualQueue,
  printingQueue,
  readOnlyQueue,
  readyQueue,
  sendingQueue,
  waitingQueue,
} from './test/fakes';

/** The queue on a Prusa printer in state, as the server answers it. */
const withState = (queue: Queue, state: string): Queue => ({
  ...queue,
  printer: {
    ...queue.printer,
    state,
    free: ['IDLE', 'READY', 'FINISHED', 'STOPPED'].includes(state),
    ready: state === 'READY',
  },
});

describe('what the active entry is doing', () => {
  it('says a file the printer already holds is starting, not on its way', () => {
    const held = {
      ...sendingQueue,
      entries: sendingQueue.entries.map((entry) =>
        entry.state === 'sending' ? { ...entry, storage: '' } : entry,
      ),
    };

    expect(outlookOf(sendingQueue)).toBe('sending');
    expect(outlookOf(held)).toBe('opening');
  });
});

describe('what the Queue tab offers', () => {
  it('offers one button that sets ready and prints while a file waits', () => {
    expect(offersOf(waitingQueue)).toEqual({
      printNext: true,
      markReady: false,
      unmarkReady: true,
      readyNow: false,
      allowAlone: true,
    });
  });

  it('shows the ready pair on a resting printer and says which one is live', () => {
    const offers = offersOf(readyQueue);

    expect(offers.unmarkReady).toBe(true);
    expect(offers.readyNow).toBe(true);
    expect(offersOf({ ...readyQueue, entries: [] }).markReady).toBe(true);
    expect(offersOf(printingQueue).unmarkReady).toBe(false);
    expect(offers.markReady).toBe(false);
    expect(offers.printNext).toBe(true);
  });

  it('offers nothing to start while a file is on its way or printing', () => {
    expect(offersOf(printingQueue).printNext).toBe(false);
    expect(offersOf(sendingQueue).printNext).toBe(false);
    expect(offersOf(sendingQueue).markReady).toBe(false);
  });

  it('offers nothing to a reader who may not change the queue', () => {
    expect(Object.values(offersOf(readOnlyQueue))).not.toContain(true);
    expect(
      Object.values(offersOf({ ...waitingQueue, may_change: false })),
    ).not.toContain(true);
  });

  it('offers only the switch for a printer that is away', () => {
    expect(offersOf(awayQueue)).toEqual({
      printNext: false,
      markReady: false,
      unmarkReady: false,
      readyNow: false,
      allowAlone: true,
    });
  });

  it('shows the switch for printing on ready on every printer that can be ready', () => {
    expect(offersOf(manualQueue).allowAlone).toBe(true);
    expect(offersOf({ ...manualQueue, entries: [] }).allowAlone).toBe(true);
    expect(offersOf(waitingQueue).allowAlone).toBe(true);
    expect(
      offersOf({
        ...manualQueue,
        printer: { ...manualQueue.printer, can_be_ready: false },
      }).allowAlone,
    ).toBe(false);
  });

  it('offers no mark on a printer without the ready command', () => {
    const queue: Queue = {
      ...waitingQueue,
      printer: { ...waitingQueue.printer, can_be_ready: false },
    };

    expect(offersOf(queue).markReady).toBe(false);
    expect(offersOf(queue).printNext).toBe(true);
  });

  it('does not offer to print on a busy printer', () => {
    expect(offersOf(withState(waitingQueue, 'PAUSED')).printNext).toBe(false);
    expect(offersOf(withState(waitingQueue, 'ERROR')).printNext).toBe(false);
  });
});

describe('a printer with words of its own', () => {
  const says = (state: string, free: boolean, ready: boolean): Queue => ({
    ...waitingQueue,
    printer: { ...waitingQueue.printer, state, free, ready },
  });

  it('is read by what the server says of its state, not by the word', () => {
    expect(offersOf(says('STANDBY', true, false)).printNext).toBe(true);
    expect(outlookOf(says('STANDBY', true, false))).toBe('markBed');
    expect(offersOf(says('ARMED', true, true)).readyNow).toBe(true);
    expect(outlookOf(says('ARMED', true, true))).toBe('starting');
    expect(offersOf(says('IDLE', false, false)).printNext).toBe(false);
    expect(outlookOf(says('IDLE', false, false))).toBe('printerBusy');
  });
});

describe('what the status card says', () => {
  it('names each case', () => {
    expect(outlookOf(emptyQueue)).toBe('empty');
    expect(outlookOf(printingQueue)).toBe('printing');
    expect(outlookOf(sendingQueue)).toBe('sending');
    expect(outlookOf(waitingQueue)).toBe('markBed');
    expect(outlookOf(readyQueue)).toBe('starting');
    expect(outlookOf(manualQueue)).toBe('pressPrint');
    expect(outlookOf(heldQueue)).toBe('held');
    expect(outlookOf(awayQueue)).toBe('away');
    expect(outlookOf(withState(waitingQueue, 'ATTENTION'))).toBe('printerBusy');
  });

  it('never says a held file starts in a moment', () => {
    for (const note of [
      'owner_may_not',
      'owner_unknown',
      'owner_unchecked',
      'no_storage',
      'not_arrived',
      'printer_not_free',
    ] as const) {
      const held: Queue = {
        ...readyQueue,
        entries: readyQueue.entries.map((entry) =>
          entry.id === waitingOf(readyQueue)[0]?.id
            ? { ...entry, note }
            : entry,
        ),
      };
      expect(outlookOf(held), note).toBe('held');
    }
  });

  it('says a printer that cannot be marked is started by hand', () => {
    const byHand = {
      ...waitingQueue,
      printer: { ...waitingQueue.printer, can_be_ready: false },
    };
    expect(outlookOf(byHand)).toBe('byHand');
    expect(outlookOf({ ...byHand, starts_alone: true })).toBe('byHand');
    expect(offersOf(byHand)).toMatchObject({
      markReady: false,
      allowAlone: false,
    });
  });
});

describe('the small words', () => {
  it('takes the files the add route takes', () => {
    expect(isPrintable('parts/a.bgcode')).toBe(true);
    expect(isPrintable('parts/A.GCODE')).toBe(true);
    expect(isPrintable('parts/a.gcode.gz')).toBe(false);
    expect(isPrintable('parts/a.3mf')).toBe(false);
    expect(isPrintable('parts/a.stl')).toBe(false);
  });

  it('calls the reader you and drops the kind of a principal', () => {
    expect(personOf('user:alice', 'user:alice')).toEqual({
      you: true,
      name: '',
    });
    expect(personOf('user:bob', 'user:alice')).toEqual({
      you: false,
      name: 'bob',
    });
    expect(personOf('bob', undefined).name).toBe('bob');
    expect(personOf(undefined, 'user:alice').name).toBe('');
  });

  it('names the folder with its storage', () => {
    expect(folderOf({ storage: 'local', path: 'parts/a.bgcode' })).toBe(
      'local/parts',
    );
    expect(folderOf({ storage: 'usb', path: 'a.bgcode' })).toBe('usb');
    expect(folderOf({ storage: '', path: 'usb/prints/a.bgcode' })).toBe(
      'usb/prints',
    );
  });

  it('keeps the waiting files in their order', () => {
    expect(waitingOf(printingQueue).map((entry) => entry.id)).toEqual([
      'e1',
      'e2',
      'e3',
    ]);
  });
});
