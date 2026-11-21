// When each file of a printer's queue prints, as plain functions over the
// queue the routes answer, so a test reads them without a browser.
//
// Every time here is the earliest one possible. A printer takes the next
// file only once somebody said its bed is clear, and nobody knows when that
// will be, so the plan assumes it happens a few minutes after each print.
import type { Entry, Queue } from './api';
import { activeOf, waitingOf } from './queue-text';

/** The pause the plan leaves between two prints, to take a print off the bed. */
export const BETWEEN_PRINTS_MS = 3 * 60 * 1000;

/** How long a file that names no print time is drawn as. */
export const UNKNOWN_DRAWN_MS = 60 * 60 * 1000;

const SECOND_MS = 1000;

/**
 * What is left of a print that has run past the slicer's time, such as one
 * that heated the bed first. It ends any moment, so it is drawn as a short
 * bar that ends soon, and not as one of unknown length.
 */
export const OVERRUN_LEFT_MS = 60 * 1000;
const HOUR_MS = 60 * 60 * 1000;

/** One file on the plan. */
export interface Slot {
  readonly entry: Entry;
  /** The file is on the printer's bed now, or on its way there. */
  readonly active: boolean;
  /** When it starts and ends at the earliest, in milliseconds since the epoch. */
  readonly start: number;
  readonly end: number;
  /** Whether its length is known. An unknown one is drawn as an hour. */
  readonly known: boolean;
  /**
   * Whether every file before it has a known length. Behind a file of unknown
   * length a start is only a lower bound.
   */
  readonly firm: boolean;
}

/** The whole queue of one printer, laid out from now on. */
export interface Plan {
  readonly slots: readonly Slot[];
  /** When the last file ends at the earliest. */
  readonly end: number;
  /** Whether that end is known, or only a lower bound. */
  readonly firm: boolean;
  /** Whether a file prints or is on its way now. Otherwise the plan starts when a person starts it. */
  readonly running: boolean;
}

/**
 * How long the active file still takes, when anybody knows. The printer's
 * own word comes first. A printer says nothing for the first moments of a
 * print, and then the slicer's time less what has run stands in. A print
 * past the slicer's time ends any moment.
 */
const activeLeft = (
  queue: Queue,
  entry: Entry,
  now: number,
): number | undefined => {
  const whole = (entry.estimated_seconds ?? 0) * SECOND_MS;
  if (entry.state !== 'printing') {
    // On its way or starting: the whole print is still ahead.
    return whole > 0 ? whole : undefined;
  }

  const left = (queue.remaining_seconds ?? 0) * SECOND_MS;
  if (left > 0) return left;

  if (whole <= 0) return undefined;

  const ran = now - Date.parse(entry.changed_at);
  const rest = whole - (Number.isFinite(ran) ? Math.max(ran, 0) : 0);

  return Math.max(rest, OVERRUN_LEFT_MS);
};

/**
 * When the active file began: when the print began for one that prints, and
 * now for one still on its way. The queue writes the print's own start on the
 * entry when it learns of it. A start in the future is read as now.
 */
const activeStart = (entry: Entry, now: number): number => {
  if (entry.state !== 'printing') return now;
  const began = Date.parse(entry.changed_at);

  return Number.isFinite(began) ? Math.min(began, now) : now;
};

/** The plan of one queue from `now`, or nothing for an empty queue. */
export const planOf = (queue: Queue, now: number): Plan | undefined => {
  const slots: Slot[] = [];
  let cursor = now;
  let firm = true;

  const active = activeOf(queue);
  if (active !== undefined) {
    const left = activeLeft(queue, active, now);
    const end = now + (left ?? UNKNOWN_DRAWN_MS);
    slots.push({
      entry: active,
      active: true,
      start: activeStart(active, now),
      end,
      known: left !== undefined,
      firm: true,
    });
    cursor = end + BETWEEN_PRINTS_MS;
    firm = left !== undefined;
  }

  for (const entry of waitingOf(queue)) {
    const whole = (entry.estimated_seconds ?? 0) * SECOND_MS;
    const known = whole > 0;
    const end = cursor + (known ? whole : UNKNOWN_DRAWN_MS);
    slots.push({ entry, active: false, start: cursor, end, known, firm });
    cursor = end + BETWEEN_PRINTS_MS;
    firm = firm && known;
  }

  const last = slots.at(-1);
  if (last === undefined) return undefined;

  return { slots, end: last.end, firm, running: active !== undefined };
};

/** The slot of each file, by the entry's id. */
export const slotsOf = (plan: Plan | undefined): ReadonlyMap<string, Slot> =>
  new Map((plan?.slots ?? []).map((slot) => [slot.entry.id, slot]));

/**
 * The scale a plan is drawn on. It is fixed: an hour is as wide on every
 * printer and for every queue, so the width of a bar always means the same
 * length of print, and a long queue is scrolled and not squeezed.
 */
export interface Axis {
  /** The moment at the left end: half a mark before now. */
  readonly from: number;
  readonly to: number;
  /** The marks, one every `hours`, from the first after `from`. */
  readonly ticks: readonly number[];
  /** Where the first whole hour sits, in pixels from the left end. */
  readonly hourAt: number;
  /** How wide the whole axis is, in pixels. */
  readonly width: number;
  /** How many hours one mark spans, which the zoom sets. */
  readonly hours: number;
}

/** A mark every three hours, 200 pixels apart, unless zoomed. */
export const MARK_HOURS = 3;
export const MARK_WIDTH = 200;
/** The axis is at least three days long, and longer for a queue that is. */
export const SHORTEST_HOURS = 72;
/**
 * The hours one mark may span, from the closest zoom to the farthest. At the
 * closest, a mark every quarter of an hour, about an hour fills a screen.
 */
export const ZOOMS = [0.25, 0.5, 1, 2, 3, 6, 12] as const;

/** The zoom one step closer, or one step farther, and the same at either end. */
export const zoomed = (hours: number, closer: boolean): number => {
  const at = ZOOMS.findIndex((one) => one === hours);
  const next = ZOOMS[closer ? at - 1 : at + 1];

  return next ?? hours;
};

/** The fewest marks an axis has, whatever the zoom: three days at the default one. */
const FEWEST_MARKS = SHORTEST_HOURS / MARK_HOURS;

/**
 * The mark at or before a moment, on the reader's own clock: with marks six
 * hours apart, midnight, six, noon and six in the evening where they live, and
 * with marks a quarter of an hour apart, each quarter of the hour.
 */
const markBefore = (at: number, hours: number): number => {
  const step = Math.round(hours * 60);
  const day = new Date(at);
  const minutes =
    Math.floor((day.getHours() * 60 + day.getMinutes()) / step) * step;
  day.setHours(Math.floor(minutes / 60), minutes % 60, 0, 0);

  return day.getTime();
};

/** The first whole step of `hours` at or after a moment. */
const markAfter = (at: number, hours: number): number => {
  const before = markBefore(at, hours);

  return before < at ? before + hours * HOUR_MS : before;
};

/**
 * The axis plans ending at `end` are drawn on from `now`, a mark every
 * `hours`. It starts half a mark, 100 pixels, before now, so some of the past
 * is in sight before the line of now, and the marks stay
 * on whole hours. A print that began before that is drawn from the left end.
 * Every plan on one page shares one, so an hour is as wide on each.
 */
export const axisOf = (
  plan: Pick<Plan, 'end'>,
  now: number,
  hours: number = MARK_HOURS,
): Axis => {
  const markMs = hours * HOUR_MS;
  const from = now - markMs / 2;
  const marks = Math.max(FEWEST_MARKS, Math.ceil((plan.end - from) / markMs));
  const to = from + marks * markMs;
  const first = markAfter(from, hours);
  const ticks: number[] = [];
  for (let tick = first; tick < to; tick += markMs) ticks.push(tick);

  return {
    from,
    to,
    ticks,
    hourAt: ((markAfter(from, 1) - from) / HOUR_MS) * (MARK_WIDTH / hours),
    width: marks * MARK_WIDTH,
    hours,
  };
};

/** Where a moment sits on an axis, in pixels from its left end. */
export const placeOf = (axis: Axis, at: number): number =>
  Math.min(
    axis.width,
    Math.max(0, ((at - axis.from) / (axis.hours * HOUR_MS)) * MARK_WIDTH),
  );

/** The hatching of a file whose length nobody knows, over its own colour. */
export const STRIPES =
  'repeating-linear-gradient(135deg, transparent 0 6px, color-mix(in oklab, currentColor 22%, transparent) 6px 12px)';
