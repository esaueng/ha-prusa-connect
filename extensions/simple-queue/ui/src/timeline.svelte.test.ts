import { describe, expect, it } from 'vitest';

import type { Entry, Queue } from './api';
import { emptyQueue, printingQueue, waitingQueue } from './test/fakes';
import {
  axisOf,
  BETWEEN_PRINTS_MS,
  MARK_HOURS,
  MARK_WIDTH,
  OVERRUN_LEFT_MS,
  placeOf,
  planOf,
  SHORTEST_HOURS,
  slotsOf,
  UNKNOWN_DRAWN_MS,
} from './timeline';

const MIN = 60 * 1000;
const HOUR = 60 * MIN;
// 09:35 on a day, so the axis has to round to whole hours on both ends.
const NOW = Date.UTC(2026, 10, 13, 9, 35);

const timed = (queue: Queue, seconds: readonly number[]): Queue => {
  let waiting = 0;

  return {
    ...queue,
    entries: queue.entries.map((entry): Entry => {
      if (entry.state !== 'waiting') return entry;
      const estimated = seconds[waiting++] ?? 0;

      return estimated > 0 ? { ...entry, estimated_seconds: estimated } : entry;
    }),
  };
};

describe('when the files of a queue print', () => {
  it('has no plan for an empty queue', () => {
    expect(planOf(emptyQueue, NOW)).toBeUndefined();
  });

  it('starts the first waiting file now and each next one after a pause', () => {
    const plan = planOf(timed(waitingQueue, [2 * 3600, 1800, 3600]), NOW);

    expect(plan?.running).toBe(false);
    expect(
      plan?.slots.map((slot) => [slot.start - NOW, slot.end - NOW]),
    ).toEqual([
      [0, 2 * HOUR],
      [2 * HOUR + BETWEEN_PRINTS_MS, 2 * HOUR + BETWEEN_PRINTS_MS + 30 * MIN],
      [
        2 * HOUR + 30 * MIN + 2 * BETWEEN_PRINTS_MS,
        3 * HOUR + 30 * MIN + 2 * BETWEEN_PRINTS_MS,
      ],
    ]);
    expect(plan?.firm).toBe(true);
    expect(plan?.end).toBe(NOW + 3 * HOUR + 30 * MIN + 2 * BETWEEN_PRINTS_MS);
  });

  it('starts behind the print that runs, by what the printer says is left', () => {
    const plan = planOf(
      { ...timed(printingQueue, [3600]), remaining_seconds: 67 * 60 },
      NOW,
    );

    expect(plan?.running).toBe(true);
    expect(plan?.slots[0]).toMatchObject({
      active: true,
      known: true,
      end: NOW + 67 * MIN,
    });
    expect(plan?.slots[1]?.start).toBe(NOW + 67 * MIN + BETWEEN_PRINTS_MS);
  });

  it('draws a file of unknown length as an hour, and every start behind it is a lower bound', () => {
    const plan = planOf(timed(waitingQueue, [3600, 0, 1800]), NOW);
    const [first, unknown, last] = plan?.slots ?? [];

    expect(first).toMatchObject({ known: true, firm: true });
    expect(unknown).toMatchObject({ known: false, firm: true });
    expect((unknown?.end ?? 0) - (unknown?.start ?? 0)).toBe(UNKNOWN_DRAWN_MS);
    expect(last).toMatchObject({ known: true, firm: false });
    expect(plan?.firm).toBe(false);
  });

  it('reads a print the printer says nothing about yet from the slicer, less what has run', () => {
    const started = new Date(NOW - 10 * MIN).toISOString();
    const queue = {
      ...printingQueue,
      entries: printingQueue.entries.map((entry) =>
        entry.state === 'printing'
          ? { ...entry, estimated_seconds: 3600, changed_at: started }
          : entry,
      ),
    };

    expect(planOf(queue, NOW)?.slots[0]).toMatchObject({
      known: true,
      end: NOW + 50 * MIN,
    });
    // The printer's own word wins once it has one.
    expect(
      planOf({ ...queue, remaining_seconds: 120 }, NOW)?.slots[0]?.end,
    ).toBe(NOW + 2 * MIN);
  });

  // The bar of the print starts when the print began,
  // ends when the printer says it will, and the queue follows.
  it('draws the print from its own start to the end the printer says', () => {
    const started = new Date(NOW - 20 * MIN).toISOString();
    const queue = {
      ...timed(printingQueue, [3600]),
      remaining_seconds: 15 * 60,
    };
    queue.entries = queue.entries.map((entry) =>
      entry.state === 'printing' ? { ...entry, changed_at: started } : entry,
    );
    const [active, next] = planOf(queue, NOW)?.slots ?? [];

    expect(active).toMatchObject({
      start: NOW - 20 * MIN,
      end: NOW + 15 * MIN,
      known: true,
    });
    expect(next?.start).toBe(NOW + 15 * MIN + BETWEEN_PRINTS_MS);
  });

  // A print runs past the slicer's time while the bed heats, and the printer
  // says nothing of what is left. It ends any moment, and its length is
  // still known.
  it('draws a print past the slicer time as ending in a minute', () => {
    const started = new Date(NOW - 5 * MIN).toISOString();
    const queue = {
      ...printingQueue,
      entries: printingQueue.entries.map((entry) =>
        entry.state === 'printing'
          ? { ...entry, estimated_seconds: 120, changed_at: started }
          : entry,
      ),
    };

    expect(planOf(queue, NOW)?.slots[0]).toMatchObject({
      active: true,
      known: true,
      end: NOW + OVERRUN_LEFT_MS,
    });
  });

  it('does not know the end of a running print nobody says the length of', () => {
    const queue = {
      ...printingQueue,
      entries: printingQueue.entries.map((entry) =>
        entry.state === 'printing' ? { ...entry, estimated_seconds: 0 } : entry,
      ),
    };
    const plan = planOf(queue, NOW);

    expect(plan?.slots[0]).toMatchObject({ active: true, known: false });
    expect(plan?.slots[1]?.firm).toBe(false);
  });

  it('finds the slot of a file by its id', () => {
    const queue = timed(waitingQueue, [3600]);
    const first = queue.entries.find((entry) => entry.state === 'waiting');

    expect(slotsOf(planOf(queue, NOW)).get(first?.id ?? '')?.start).toBe(NOW);
    expect(slotsOf(undefined).size).toBe(0);
  });
});

describe('the axis a plan is drawn on', () => {
  // 09:35 on the reader's own clock, because the marks stand at their
  // midnight, six, noon and six in the evening.
  const local = new Date(2026, 10, 13, 9, 35).getTime();
  const at = (day: number, hour: number): number =>
    new Date(2026, 10, day, hour).getTime();

  it('has one scale for every queue: a mark each three hours, 200 pixels apart', () => {
    const short = planOf(timed(waitingQueue, [1800]), local);
    const long = planOf(timed(waitingQueue, [40 * 3600]), local);
    if (short === undefined || long === undefined) {
      throw new Error('the queues have files');
    }

    for (const plan of [short, long]) {
      const axis = axisOf(plan, local);

      expect(axis.from).toBe(local - (MARK_HOURS / 2) * HOUR);
      expect(axis.ticks[0]).toBe(at(13, 9));
      expect((axis.ticks[1] ?? 0) - (axis.ticks[0] ?? 0)).toBe(
        MARK_HOURS * HOUR,
      );
      // An hour is the same width whatever the queue holds.
      expect(placeOf(axis, at(13, 11)) - placeOf(axis, at(13, 10))).toBeCloseTo(
        MARK_WIDTH / MARK_HOURS,
        5,
      );
    }
  });

  it('is three days long for a short queue, and as long as a longer queue', () => {
    const short = planOf(timed(waitingQueue, [1800]), local);
    const week = planOf(timed(waitingQueue, [100 * 3600]), local);
    if (short === undefined || week === undefined) {
      throw new Error('the queues have files');
    }

    const three = axisOf(short, local);
    expect(three.to - three.from).toBe(SHORTEST_HOURS * HOUR);
    expect(three.width).toBe((SHORTEST_HOURS / MARK_HOURS) * MARK_WIDTH);
    expect(three.ticks).toHaveLength(SHORTEST_HOURS / MARK_HOURS);

    const longer = axisOf(week, local);
    expect(longer.to).toBeGreaterThanOrEqual(week.end);
    expect(longer.to - week.end).toBeLessThan(MARK_HOURS * HOUR);
    expect(longer.width).toBeGreaterThan(three.width);
  });

  // The line of now stands 100 pixels from the left
  // end, whatever the zoom and the time, and the marks stay on whole hours.
  it('starts 100 pixels before now, with its marks on whole hours', () => {
    const justAfter = new Date(2026, 10, 13, 9, 3).getTime();

    for (const hours of [1, 6]) {
      const axis = axisOf({ end: justAfter + HOUR }, justAfter, hours);

      expect(placeOf(axis, justAfter)).toBeCloseTo(MARK_WIDTH / 2, 5);
      expect(new Date(axis.ticks[0] ?? 0).getMinutes()).toBe(0);
      expect(axis.ticks[0]).toBeGreaterThanOrEqual(axis.from);
    }
    // The hour ticks start at the first whole hour, 8:33 to 9:00.
    expect(axisOf({ end: justAfter + HOUR }, justAfter, 1).hourAt).toBeCloseTo(
      (27 / 60) * MARK_WIDTH,
      5,
    );
  });

  // Zoomed closest, a mark stands every quarter of an hour, on the quarter,
  // so about an hour fills a screen and a one minute print is a bar.
  it('marks every quarter of an hour at the closest zoom', () => {
    const justAfter = new Date(2026, 10, 13, 9, 3).getTime();
    const axis = axisOf({ end: justAfter + HOUR }, justAfter, 0.25);

    expect(placeOf(axis, justAfter)).toBeCloseTo(MARK_WIDTH / 2, 5);
    expect(
      axis.ticks
        .slice(0, 4)
        .map((tick) => [
          new Date(tick).getHours(),
          new Date(tick).getMinutes(),
        ]),
    ).toEqual([
      [9, 0],
      [9, 15],
      [9, 30],
      [9, 45],
    ]);
    expect(
      placeOf(axis, justAfter + HOUR) - placeOf(axis, justAfter),
    ).toBeCloseTo(MARK_WIDTH * 4, 5);
  });

  it('places a moment in pixels from the left end, and never outside the axis', () => {
    const plan = planOf(timed(waitingQueue, [1800]), local);
    if (plan === undefined) throw new Error('the queue has files');
    const axis = axisOf(plan, local);

    expect(placeOf(axis, axis.from)).toBe(0);
    expect(placeOf(axis, local)).toBe(MARK_WIDTH / 2);
    expect(placeOf(axis, axis.from - HOUR)).toBe(0);
    expect(placeOf(axis, axis.to + HOUR)).toBe(axis.width);
  });
});
