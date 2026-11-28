import { STLLoader } from 'three/addons/loaders/STLLoader.js';

import { MAX_TRIANGLES, ModelLimitError, PLACED_HEADER } from './import-limits';
import type { MeshReader } from './mesh-import';

const HEADER_BYTES = 84;
const TRIANGLE_BYTES = 50;

const faceCount = (bytes: Uint8Array): number =>
  bytes.length < HEADER_BYTES
    ? 0
    : new DataView(bytes.buffer, bytes.byteOffset).getUint32(80, true);

const asciiHeader = (bytes: Uint8Array): boolean =>
  // STLLoader recognises the signature at any of the first five byte offsets.
  new TextDecoder('windows-1252')
    .decode(bytes.subarray(0, 9))
    .includes('solid');

const binaryLength = (bytes: Uint8Array): number =>
  HEADER_BYTES + faceCount(bytes) * TRIANGLE_BYTES;

const isASCII = (bytes: Uint8Array): boolean =>
  binaryLength(bytes) !== bytes.length && asciiHeader(bytes);

export const stlReader: MeshReader = {
  matches: (bytes) =>
    bytes.length >= 15 &&
    (asciiHeader(bytes) ||
      (faceCount(bytes) > 0 && binaryLength(bytes) <= bytes.length)),
  read(bytes) {
    if (isASCII(bytes)) {
      const text = new TextDecoder().decode(bytes);
      const pattern = /vertex/gu;
      let vertices = 0;
      while (pattern.exec(text)) {
        vertices += 1;
        if (vertices > 3 * MAX_TRIANGLES)
          throw new ModelLimitError('STL has more than 300,000 triangles');
      }
    } else if (faceCount(bytes) > MAX_TRIANGLES) {
      throw new ModelLimitError('STL has more than 300,000 triangles');
    }
    const buffer =
      bytes.buffer instanceof ArrayBuffer &&
      bytes.byteOffset === 0 &&
      bytes.byteLength === bytes.buffer.byteLength
        ? bytes.buffer
        : Uint8Array.from(bytes).buffer;
    return {
      parts: [{ geometry: new STLLoader().parse(buffer) }],
      preservePlacement: PLACED_HEADER.every(
        (value, index) => bytes[index] === value,
      ),
    };
  },
};
