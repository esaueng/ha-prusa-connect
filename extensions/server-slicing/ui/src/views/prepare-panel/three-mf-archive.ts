import { ModelLimitError } from './import-limits';

const MAX_EXPANDED_BYTES = 64 * 1024 * 1024;
const MAX_ENTRIES = 4096;
const EOCD = 0x06054b50;
const CENTRAL_FILE = 0x02014b50;
export const LOCAL_FILE = 0x04034b50;
const ZIP_STORED = 0;
const ZIP_DEFLATE = 8;

interface Entry {
  offset: number;
  size: number;
  compressedSize: number;
  method: number;
  crc: number;
  flags: number;
  name: string;
}

const invalidZip = (): Error => new Error('Invalid 3MF ZIP archive');

const endOfDirectory = (view: DataView): number => {
  // The end record is 22 bytes plus a comment of at most 65,535 bytes.
  for (
    let offset = view.byteLength - 22;
    offset >= Math.max(0, view.byteLength - 22 - 65535);
    offset -= 1
  ) {
    if (
      view.getUint32(offset, true) === EOCD &&
      offset + 22 + view.getUint16(offset + 20, true) === view.byteLength
    )
      return offset;
  }
  throw invalidZip();
};

const crcTable = Uint32Array.from({ length: 256 }, (_, byte) => {
  for (let bit = 0; bit < 8; bit += 1)
    byte = (byte >>> 1) ^ (byte & 1 ? 0xedb88320 : 0);
  return byte;
});

const crc32 = (bytes: Uint8Array): number => {
  let crc = 0xffffffff;
  for (const byte of bytes)
    crc = (crc >>> 8) ^ (crcTable[(crc ^ byte) & 0xff] ?? 0);
  return (crc ^ 0xffffffff) >>> 0;
};

const inflate = async (
  compressed: Uint8Array,
  size: number,
): Promise<Uint8Array> => {
  let offset = 0;
  const source = new ReadableStream<BufferSource>({
    pull(controller) {
      // Limit how much compressed data the inflater sees in one push.
      const next = Math.min(offset + 4096, compressed.length);
      if (offset < next) controller.enqueue(compressed.slice(offset, next));
      offset = next;
      if (offset === compressed.length) controller.close();
    },
  });
  const reader = source
    .pipeThrough(new DecompressionStream('deflate-raw'))
    .getReader();
  const expanded = new Uint8Array(size);
  let written = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (written + value.length > size)
        throw new ModelLimitError('3MF expands beyond its declared size');
      expanded.set(value, written);
      written += value.length;
    }
  } finally {
    void reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
  if (written !== size) throw invalidZip();
  return expanded;
};

/** Reads only the parts used by Prepare from a single-disk OPC ZIP archive. */
export class ThreeMFArchive {
  #bytes: Uint8Array;
  #view: DataView;
  #directory: number;
  #entries = new Map<string, Entry>();

  constructor(bytes: Uint8Array) {
    this.#bytes = bytes;
    this.#view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
    const view = this.#view;
    const end = endOfDirectory(view);
    const count = view.getUint16(end + 10, true);
    const length = view.getUint32(end + 12, true);
    this.#directory = view.getUint32(end + 16, true);
    if (
      view.getUint32(end + 4, true) !== 0 ||
      view.getUint16(end + 8, true) !== count ||
      this.#directory + length !== end
    )
      throw invalidZip();
    if (count > MAX_ENTRIES)
      throw new ModelLimitError('Too many 3MF archive entries');

    const decoder = new TextDecoder('utf-8', { fatal: true });
    let total = 0;
    let at = this.#directory;
    for (let i = 0; i < count; i += 1) {
      if (at + 46 > end || view.getUint32(at, true) !== CENTRAL_FILE)
        throw invalidZip();
      const nameEnd = at + 46 + view.getUint16(at + 28, true);
      const next =
        nameEnd + view.getUint16(at + 30, true) + view.getUint16(at + 32, true);
      if (next > end || view.getUint16(at + 34, true) !== 0) throw invalidZip();

      const name = decoder.decode(bytes.subarray(at + 46, nameEnd));
      const key = name.toLowerCase();
      const entry = {
        offset: view.getUint32(at + 42, true),
        size: view.getUint32(at + 24, true),
        compressedSize: view.getUint32(at + 20, true),
        method: view.getUint16(at + 10, true),
        crc: view.getUint32(at + 16, true),
        flags: view.getUint16(at + 8, true),
        name,
      };
      if (this.#entries.has(key)) throw invalidZip();
      this.#entries.set(key, entry);
      at = next;
      total += entry.size;
      if (total > MAX_EXPANDED_BYTES)
        throw new ModelLimitError('3MF archive exceeds 64 MiB when expanded');
    }
    if (at !== end) throw invalidZip();
  }

  async read(name: string): Promise<Uint8Array | undefined> {
    const entry = this.#entries.get(name.toLowerCase());
    if (!entry) return undefined;
    const { offset, size, compressedSize, method, crc, flags } = entry;
    const view = this.#view;
    if (
      offset + 30 > this.#directory ||
      view.getUint32(offset, true) !== LOCAL_FILE ||
      view.getUint16(offset + 6, true) !== flags ||
      view.getUint16(offset + 8, true) !== method ||
      (flags & 1) !== 0
    )
      throw invalidZip();

    const nameEnd = offset + 30 + view.getUint16(offset + 26, true);
    const start = nameEnd + view.getUint16(offset + 28, true);
    const end = start + compressedSize;
    if (
      end > this.#directory ||
      new TextDecoder('utf-8', { fatal: true }).decode(
        this.#bytes.subarray(offset + 30, nameEnd),
      ) !== entry.name
    )
      throw invalidZip();

    if (method !== ZIP_STORED && method !== ZIP_DEFLATE) throw invalidZip();
    const compressed = this.#bytes.subarray(start, end);
    const data =
      method === ZIP_STORED ? compressed : await inflate(compressed, size);
    if (data.length !== size || crc32(data) !== crc) throw invalidZip();
    return data;
  }
}
