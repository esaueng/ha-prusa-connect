// The picture inside a queued print file, by the address the core serves it
// at. A file in a storage is read on the server. A file that is only on the
// printer is read from the printer, the first time it is asked for.
//
// A module-level holder, as the session is: the setup function fills it from
// the host's client before any view is mounted. A view drawn without it, in a
// test or a story, gets no address and draws the file's glyph.
import { deviceThumbnailUrlOf, thumbnailUrlOf } from '@prusa/connect-ui/data';

import type { Entry } from './api';

/** The server the pictures are asked of. */
type Client = Parameters<typeof thumbnailUrlOf>[0];

/** What names a queued file: its storage and path there, or its path on the printer. */
export type Pictured = Pick<Entry, 'storage' | 'path'> & {
  size?: number | undefined;
  /** Seconds since the Unix epoch, or the time as a listing writes it. */
  modified?: number | string | undefined;
};

let held: Client | undefined;

/** Called once by the module's setup function. */
export const holdThumbnails = (client: Client): void => {
  held = client;
};

/** The address of a queued file's picture, on the printer the queue is of. */
export const thumbnailOf = (
  device: string,
  entry: Pictured,
): string | undefined => {
  if (held === undefined) return undefined;

  if (entry.storage !== '') {
    return thumbnailUrlOf(held, entry.storage, entry.path);
  }

  // The size and the time name the version of a file on the printer. The
  // server answers a version it holds the picture of without asking the
  // printer, and the address changes once the queue learns the version, so a
  // picture refused before the file had landed is asked for again.
  if (!entry.size) {
    return deviceThumbnailUrlOf(held, device, entry.path);
  }

  const modified =
    typeof entry.modified === 'number'
      ? new Date(entry.modified * 1000).toISOString()
      : entry.modified;

  return deviceThumbnailUrlOf(held, device, entry.path, {
    size: entry.size,
    ...(modified === undefined ? {} : { modified }),
  });
};
