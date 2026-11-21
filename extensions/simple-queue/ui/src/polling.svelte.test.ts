import { afterEach, describe, expect, it, vi } from 'vitest';

import { poll, REFRESH_MS } from './polling';

const hide = (hidden: boolean): void => {
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(hidden);
  document.dispatchEvent(new Event('visibilitychange'));
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('reading a queue again', () => {
  // An event wakes a read at once. Events that arrive while one is on its way
  // cost one more read after it, however many there are.
  it('reads at once when woken, and once more for a burst during a read', async () => {
    vi.useFakeTimers();
    let finish: (() => void) | undefined;
    const read = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const stop = new AbortController();

    const wake = poll(read, stop.signal);
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(1);

    wake();
    wake();
    wake();
    finish?.();
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(2);

    finish?.();
    await vi.advanceTimersByTimeAsync(0);
    wake();
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(3);

    stop.abort();
  });

  it('waits for one read to end before it waits for the next', async () => {
    vi.useFakeTimers();
    let finish: (() => void) | undefined;
    const read = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const stop = new AbortController();

    poll(read, stop.signal);
    await vi.advanceTimersByTimeAsync(REFRESH_MS * 3);
    expect(read).toHaveBeenCalledTimes(1);

    finish?.();
    await vi.advanceTimersByTimeAsync(REFRESH_MS - 1);
    expect(read).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(read).toHaveBeenCalledTimes(2);

    stop.abort();
  });

  it('reads nothing while the page is hidden, and at once when it is shown', async () => {
    vi.useFakeTimers();
    const read = vi.fn(() => Promise.resolve());
    const stop = new AbortController();

    poll(read, stop.signal);
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(1);

    hide(true);
    await vi.advanceTimersByTimeAsync(REFRESH_MS * 4);
    expect(read).toHaveBeenCalledTimes(1);

    hide(false);
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(2);

    stop.abort();
    await vi.advanceTimersByTimeAsync(REFRESH_MS * 4);
    hide(false);
    expect(read).toHaveBeenCalledTimes(2);
  });
});
