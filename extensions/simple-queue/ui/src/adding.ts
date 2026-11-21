// Putting one file on a printer's queue: from the print dialog, from a file's
// menu on the printer, or from the Add files dialog. Each printer and each
// file answers on its own, so one full queue does not stop the rest.
import { describeFault, type FaultText } from '@prusa/connect-ui/i18n';

import type { Entry, Queue, QueueApi } from './api';

/**
 * Where a file lies: in a storage of this server, or already on the printer
 * when no storage is named, with the path the printer reports, such as
 * usb/bracket.bgcode.
 */
export interface FileAt {
  storage?: string | undefined;
  path: string;
  /** True for a file still being copied to that path on the printer. */
  arriving?: boolean | undefined;
}

/** How adding the file to one printer's queue ended. */
export type Added =
  | { kind: 'added'; position: number }
  | { kind: 'started' }
  | { kind: 'held'; note: NonNullable<Entry['note']> }
  | { kind: 'failed'; fault: FaultText };

/**
 * Where the file landed in the answer: printing at once, held by a note when
 * the start that followed the add failed, or its place among the waiting
 * files counted from 1. The newest entry of the file is the one just added,
 * because a file may wait on one queue twice.
 */
export const placeOf = (queue: Queue, file: FileAt): Added => {
  const same = queue.entries
    .map((entry, index) => ({ entry, index }))
    .filter(
      ({ entry }) =>
        entry.storage === (file.storage ?? '') && entry.path === file.path,
    )
    .sort((a, b) => b.entry.added_at.localeCompare(a.entry.added_at));
  const newest = same[0];
  if (newest === undefined)
    return { kind: 'added', position: queue.entries.length };
  if (newest.entry.state !== 'waiting') return { kind: 'started' };
  if (newest.entry.note !== undefined) {
    return { kind: 'held', note: newest.entry.note };
  }

  const ahead = queue.entries
    .slice(0, newest.index)
    .filter((entry) => entry.state === 'waiting').length;

  return { kind: 'added', position: ahead + 1 };
};

/** Adds the file to one printer's queue, and says how that ended. */
export const addTo = async (
  api: Pick<QueueApi, 'addEntry'>,
  device: string,
  file: FileAt,
): Promise<Added> => {
  try {
    const queue = await api.addEntry({
      path: { device },
      body: bodyOf(file),
    });

    return placeOf(queue, file);
  } catch (thrown) {
    return { kind: 'failed', fault: describeFault(thrown) };
  }
};

/** The add route's body: a file on the printer names no storage. */
export const bodyOf = (
  file: FileAt,
): { storage?: string; path: string; arriving?: boolean } => {
  if (file.storage !== undefined)
    return { storage: file.storage, path: file.path };

  return file.arriving === true
    ? { path: file.path, arriving: true }
    : { path: file.path };
};
