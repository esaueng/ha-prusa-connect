import {
  landingPathOf,
  NoPrinterStorage,
  printFile,
  readDeviceStorages,
} from '@prusa/connect-ui/data';
import { parseFileRef, type ModuleHost } from '@prusa/connect-ui/slots';

import type { Job, SlicerApi } from '../../api';
import type { PrintSettings } from '../../plate/print-settings';
import type { SendGuard } from '../../plate/sendable';
import { MAX_MODEL_BYTES, ModelLimitError } from './import-limits';

export const placedName = (fileName: string): string => {
  const clean = fileName
    .replace(/\.[^.]+$/u, '')
    .replace(/[^\p{L}\p{N}._-]/gu, '-');
  const stem = Array.from(clean).slice(0, 40).join('') || 'model';
  const suffix = Array.from(
    crypto.getRandomValues(new Uint8Array(16)),
    (byte) => byte.toString(16).padStart(2, '0'),
  ).join('');
  return `${stem}-placed-${suffix}.stl`;
};

export interface Destination {
  name: string;
  label: string;
}

/** One sliced plate to print, from the storage the job wrote it to. */
export interface PrintRequest {
  deviceId: string;
  storage: string;
  /** The G-code's path in that storage. */
  source: string;
  /** The file's name on the printer. */
  name: string;
}

/** The code a printer that refused the start is answered with. */
export const COMMAND_REFUSED = 'core.printer/COMMAND_REFUSED';

export interface PrepareApi {
  readonly destinations: (signal: AbortSignal) => Promise<Destination[]>;
  readonly read: (ref: string, signal: AbortSignal) => Promise<File>;
  readonly save: (
    storage: string,
    name: string,
    body: Blob,
    signal: AbortSignal,
  ) => Promise<string>;
  readonly submit: (
    request: {
      storage: string;
      path: string;
      profile: string;
      settings: PrintSettings | undefined;
    },
    signal: AbortSignal,
  ) => Promise<Job>;
  /** One job as it is now. */
  readonly job: (id: string, signal: AbortSignal) => Promise<Job>;
  /** A file from a storage, such as the G-code a job wrote. */
  readonly download: (
    storage: string,
    path: string,
    signal: AbortSignal,
  ) => Promise<Blob>;
  /**
   * Sends the G-code to an idle printer and starts it. A refusal throws.
   * `guard` runs after every wait, right before the next request, and throws
   * when the page closed or the plate may no longer be sent.
   */
  readonly print: (
    request: PrintRequest,
    signal: AbortSignal,
    guard: SendGuard,
  ) => Promise<void>;
}

const readFile = async (response: Response, name: string): Promise<File> => {
  const reader = response.body?.getReader();
  if (!reader) throw new Error('The storage gave no file');

  let size = 0;
  const chunks: Uint8Array<ArrayBuffer>[] = [];
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return new File(chunks, name);
      size += value.byteLength;
      if (size > MAX_MODEL_BYTES)
        throw new ModelLimitError('Model exceeds 20 MiB');
      chunks.push(Uint8Array.from(value));
    }
  } finally {
    void reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
};

export const prepareOn = (
  client: ModuleHost['client'],
  api: Pick<SlicerApi, 'submitJob' | 'readJob'>,
): PrepareApi => ({
  async destinations(signal) {
    const page = await client.listStorages({}, signal);
    return (page.items ?? [])
      .filter(
        (item) =>
          item.browse &&
          item.write &&
          (item.allowed_extensions.length === 0 ||
            (item.allowed_extensions.includes('.stl') &&
              item.allowed_extensions.includes('.gcode'))),
      )
      .map((item) => ({ name: item.name, label: item.display_name }));
  },
  async read(ref, signal) {
    const file = parseFileRef(ref);
    if (!file) throw new Error('Invalid storage file');
    const response = await client.downloadStorageObject(
      { path: { storage: file.storage }, query: { path: file.path } },
      signal,
    );
    return readFile(response, file.path.split('/').at(-1) ?? 'model.stl');
  },
  async save(storage, name, body, signal) {
    const result = await client.uploadStorageObject(
      { path: { storage }, query: { path: name }, body },
      signal,
    );
    return result.path;
  },
  submit({ storage, path, profile, settings }, signal) {
    return api.submitJob(
      {
        body: {
          source: { storage, path },
          profile,
          keep_placement: true,
          format: 'gcode',
          // The STL has a fresh name each time the plate changes, so its
          // G-code is only ever this plate's.
          overwrite: true,
          ...(settings ? { settings } : {}),
        },
      },
      signal,
    );
  },
  job(id, signal) {
    return api.readJob({ path: { id } }, signal);
  },
  async download(storage, path, signal) {
    const response = await client.downloadStorageObject(
      { path: { storage }, query: { path } },
      signal,
    );
    return response.blob();
  },
  // The library's printStoredFile takes no guard, so its two steps are written
  // out here: the guard runs after the lookup, right before the print starts.
  async print({ deviceId, storage, source, name }, signal, guard) {
    guard();
    const page = await readDeviceStorages(client, deviceId, {
      refresh: true,
      signal,
    });
    const path = page.reported
      ? landingPathOf(page.items ?? [], name)
      : undefined;
    guard();
    if (path === undefined) throw new NoPrinterStorage(deviceId, page.reported);
    const outcome = await printFile(client, {
      deviceId,
      path,
      source,
      storage,
    });
    if (!outcome.ok) {
      // describeFault reads a code and a message from a thrown value.
      throw Object.assign(new Error(outcome.machineReason ?? ''), {
        code: outcome.faultCode ?? COMMAND_REFUSED,
      });
    }
  },
});
