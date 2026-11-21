import { describe, expect, it } from 'vitest';

import { clockOf, sameDay } from './clock';

describe('a moment of a plan as a person reads it', () => {
  const now = new Date(2026, 10, 13, 9, 35).getTime();

  it('is the time alone today, and names the day on any other day', () => {
    const today = clockOf(new Date(2026, 10, 13, 16, 13).getTime(), now);
    const later = clockOf(new Date(2026, 10, 16, 1, 13).getTime(), now);

    expect(today).toMatch(/13/u);
    expect(today).not.toMatch(/Nov|Mon|Fri/u);
    expect(later).toMatch(/16/u);
    expect(later).toMatch(/Nov/u);
    expect(later.length).toBeGreaterThan(today.length);
  });

  it("tells two days apart by the reader's own calendar", () => {
    expect(sameDay(now, new Date(2026, 10, 13, 23, 59).getTime())).toBe(true);
    expect(sameDay(now, new Date(2026, 10, 14, 0, 0).getTime())).toBe(false);
  });
});
