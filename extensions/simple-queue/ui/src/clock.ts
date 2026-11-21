// A moment of a queue's plan as a person reads it: the time alone for a
// moment today, and the day with it for any other day, because a queue of
// long prints runs over several days and "10:09" alone would read as today.
import { formatDate } from '@prusa/connect-ui/i18n';

/** Whether two moments fall on one day of the reader's own calendar. */
export const sameDay = (a: number, b: number): boolean => {
  const one = new Date(a);
  const other = new Date(b);

  return (
    one.getFullYear() === other.getFullYear() &&
    one.getMonth() === other.getMonth() &&
    one.getDate() === other.getDate()
  );
};

/** A moment beside `now`: its time, and its day too when that is not today. */
export const clockOf = (at: number, now: number): string =>
  sameDay(at, now)
    ? formatDate(at, { timeStyle: 'short' })
    : formatDate(at, {
        weekday: 'short',
        day: 'numeric',
        month: 'short',
        hour: 'numeric',
        minute: '2-digit',
      });
