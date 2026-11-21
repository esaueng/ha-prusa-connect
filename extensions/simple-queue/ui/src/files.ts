// Where a person picks files for a queue from: the printer's own drives, or
// one of this server's storages. Both are read through the core's routes as
// the person on the page, so a place they may not read answers a refusal.
import type { PrusaConnect } from '@prusa/connect';

import type { FileAt } from './adding';
import { isPrintable } from './queue-text';

/** The core's routes the picker reads. */
export type FilesApi = Pick<
  PrusaConnect,
  'listDeviceFiles' | 'listStorages' | 'listStorageFiles'
>;

/** A place files are picked from. */
export type Place =
  { kind: 'printer' } | { kind: 'storage'; storage: string; title: string };

/** One entry of a folder, as the picker draws it. */
export interface Row {
  /** What a person reads. */
  name: string;
  /** What the place addresses the entry by. */
  path: string;
  folder: boolean;
  size: number;
  modified: string | undefined;
  /** The folder the entry is in, when the list holds files of many folders. */
  where?: string;
}

/** The printer, which the picker opens on. */
export const PRINTER: Place = { kind: 'printer' };

/** A key that tells places apart. */
export const keyOf = (place: Place): string =>
  place.kind === 'printer' ? 'printer' : `storage:${place.storage}`;

/** The printer first, then every storage of this server whose files can be listed. */
export const placesOf = async (
  api: FilesApi,
  signal?: AbortSignal,
): Promise<Place[]> => {
  const listed = await api.listStorages({}, signal);
  const storages = (listed.items ?? [])
    .filter((storage) => storage.browse)
    .map((storage): Place => ({
      kind: 'storage',
      storage: storage.name,
      title: storage.display_name || storage.name,
    }));

  return [PRINTER, ...storages];
};

// Folders first, then the files by name, the way the core's own listings
// order them. Only a print file is offered, because the queue refuses the rest.
const ordered = (rows: Row[]): Row[] =>
  rows
    .filter((row) => row.folder || isPrintable(row.path))
    .sort((a, b) =>
      a.folder === b.folder
        ? a.name.localeCompare(b.name)
        : Number(b.folder) - Number(a.folder),
    );

// How long a folder the printer is still reading is waited for: a second at a
// time, half a minute in all, as long as a slow drive takes to answer.
const READ_PAUSE_MS = 1000;
const READ_WAITS = 30;

const pause = (ms: number, signal?: AbortSignal): Promise<void> =>
  new Promise((resolve, reject) => {
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener(
      'abort',
      () => {
        clearTimeout(timer);
        reject(signal.reason as Error);
      },
      { once: true },
    );
  });

/** What one folder of a place holds. The empty directory is the place's root. */
export const rowsOf = async (
  api: FilesApi,
  device: string,
  place: Place,
  directory: string,
  signal?: AbortSignal,
): Promise<Row[]> => {
  if (place.kind === 'printer') {
    let listed = await api.listDeviceFiles(
      { path: { device_id: device }, query: { path: directory } },
      signal,
    );
    // A printer the server has not read yet answers with nothing while it
    // reads, so the folder is asked again until the read lands.
    for (let wait = 0; listed.reading === true && wait < READ_WAITS; wait++) {
      await pause(READ_PAUSE_MS, signal);
      listed = await api.listDeviceFiles(
        { path: { device_id: device }, query: { path: directory } },
        signal,
      );
    }

    return ordered(
      (listed.items ?? []).map((file) => ({
        name: file.display_name ?? file.name,
        path: file.path,
        folder: file.folder,
        size: file.size,
        modified: file.modified,
      })),
    );
  }

  const listed = await api.listStorageFiles(
    { path: { storage: place.storage }, query: { path: directory } },
    signal,
  );

  return ordered(
    (listed.items ?? []).map((entry) => ({
      name: entry.name,
      path: entry.path,
      folder: entry.folder,
      size: entry.size,
      modified: entry.modified,
    })),
  );
};

// How many folders one read of a whole place opens. A printer answers one
// folder at a time, so a drive with more folders than this lists a part.
const FOLDERS = 200;

/** Every print file of a place, whatever folder it is in. */
export interface Found {
  rows: Row[];
  /** The place's only top folder, a printer's one drive, or ''. */
  root: string;
  /** Whether the place has more folders than one read opens. */
  partial: boolean;
}

export const filesUnder = async (
  api: FilesApi,
  device: string,
  place: Place,
  signal?: AbortSignal,
): Promise<Found> => {
  const folders = [''];
  const files: Row[] = [];
  let root = '';
  for (let at = 0; at < folders.length && at < FOLDERS; at++) {
    const directory = folders[at] ?? '';
    const rows = await rowsOf(api, device, place, directory, signal);
    const only = rows.length === 1 ? rows[0] : undefined;
    if (directory === '' && only?.folder) root = only.path;
    for (const row of rows) {
      if (row.folder) folders.push(row.path);
      else files.push({ ...row, where: directory });
    }
  }

  return {
    rows: files.sort((a, b) => a.name.localeCompare(b.name)),
    root,
    partial: folders.length > FOLDERS,
  };
};

/** The folders from the root down to directory, each with its own path. */
export const trailOf = (
  directory: string,
): { name: string; path: string }[] => {
  const parts = directory.split('/').filter((part) => part !== '');

  return parts.map((name, index) => ({
    name,
    path: parts.slice(0, index + 1).join('/'),
  }));
};

/** The file a row names, as the queue's add route takes it. */
export const fileAtOf = (place: Place, row: Row): FileAt =>
  place.kind === 'printer'
    ? { path: row.path }
    : { storage: place.storage, path: row.path };
