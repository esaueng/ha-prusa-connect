import { describe, expect, it } from 'vitest';
import { render } from 'vitest-browser-svelte';

import { t, waitingQueue } from '../../test/fakes';
import { BETWEEN_PRINTS_MS, type Slot } from '../../timeline';
import TimelineLanes from './timeline-lanes.svelte';

const MIN = 60 * 1000;

describe('the lanes of the timeline', () => {
  // A print of a minute is a few pixels wide. Its bar ends there, so the gap
  // to the next one stays the colour of the page and the bars never overlap.
  it('draws a short print no wider than its time, with the gap after it empty', async () => {
    const now = new Date(2026, 10, 13, 9, 0).getTime();
    const [first, second] = waitingQueue.entries;
    if (first === undefined || second === undefined) {
      throw new Error('the fake queue has two files');
    }
    const slot = (entry: typeof first, start: number): Slot => ({
      entry,
      active: false,
      start,
      end: start + MIN,
      known: true,
      firm: true,
    });
    const slots = [
      slot(first, now + MIN),
      slot(second, now + 2 * MIN + BETWEEN_PRINTS_MS),
    ];

    const screen = await render(TimelineLanes, {
      lanes: [{ key: 'mk4', slots }],
      end: now + 10 * MIN,
      now,
      hours: 1,
      t,
    });

    const [a, b] = Array.from(
      screen.container.querySelectorAll('[data-part="timeline-slot"]'),
    ).map((bar) => bar.getBoundingClientRect());
    expect(a?.width).toBeLessThan(5);
    expect((b?.left ?? 0) - (a?.right ?? 0)).toBeGreaterThan(5);
  });
});
