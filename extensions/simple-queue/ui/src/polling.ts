// How the pages read their queues again: one read at a time, a pause after
// each, and none while the page is hidden. The server's events wake a read
// sooner; the pause is what catches a change no event tells of, such as a
// file another person added.

/** How long a page waits after one read ends before it reads again. */
export const REFRESH_MS = 30_000;

/**
 * Reads now, and again REFRESH_MS after each read ends, until signal aborts.
 * A hidden page does not read, and reads at once when it is shown again.
 * read must not throw: it keeps its own failure.
 *
 * The answer reads at once. A call while a read is on its way reads once more
 * after it, so a burst of events costs at most two reads.
 */
export function poll(
  read: () => Promise<void>,
  signal: AbortSignal,
  ms = REFRESH_MS,
): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let reading = false;
  let again = false;
  // A function, because both change while a read is on its way.
  const quiet = (): boolean => signal.aborted || document.hidden;

  const run = async (): Promise<void> => {
    if (reading) {
      again = true;

      return;
    }
    clearTimeout(timer);
    timer = undefined;
    if (quiet()) return;
    reading = true;
    try {
      await read();
    } finally {
      reading = false;
    }
    if (again) {
      again = false;
      void run();

      return;
    }
    if (!quiet()) timer = setTimeout(() => void run(), ms);
  };
  const shown = (): void => {
    if (timer === undefined && !reading) void run();
  };

  document.addEventListener('visibilitychange', shown);
  signal.addEventListener(
    'abort',
    () => {
      clearTimeout(timer);
      document.removeEventListener('visibilitychange', shown);
    },
    { once: true },
  );
  void run();

  return () => void run();
}
