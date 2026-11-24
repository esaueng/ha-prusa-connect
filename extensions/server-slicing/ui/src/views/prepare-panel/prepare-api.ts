import { parseFileRef, type ModuleHost } from '@prusa/connect-ui/slots';

import type { Job, SlicerApi } from '../../api';

export const MAX_MODEL_BYTES = 20 * 1024 * 1024;

export const placedName = (fileName: string): string => {
  const clean = fileName.slice(0, -4).replace(/[^\p{L}\p{N}._-]/gu, '-');
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

export interface PrepareApi {
  destinations(signal: AbortSignal): Promise<Destination[]>;
  read(ref: string, signal: AbortSignal): Promise<File>;
  save(
    storage: string,
    name: string,
    body: Blob,
    signal: AbortSignal,
  ): Promise<string>;
  submit(
    storage: string,
    path: string,
    profile: string,
    signal: AbortSignal,
  ): Promise<Job>;
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
      if (size > MAX_MODEL_BYTES) throw new Error('The STL is too large');
      chunks.push(Uint8Array.from(value));
    }
  } finally {
    void reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
};

export const prepareOn = (
  client: ModuleHost['client'],
  api: Pick<SlicerApi, 'submitJob'>,
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
  submit(storage, path, profile, signal) {
    return api.submitJob(
      {
        body: {
          source: { storage, path },
          profile,
          keep_placement: true,
          format: 'gcode',
        },
      },
      signal,
    );
  },
});
