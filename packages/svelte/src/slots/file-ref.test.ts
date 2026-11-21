import { describe, expect, it } from 'vitest';

import {
  fileRefOf,
  parseFileRef,
  parsePrinterFileRef,
  printerFileRefOf,
} from './file-ref';

describe('a file reference', () => {
  it('joins the storage and the path with one colon', () => {
    expect(fileRefOf('local', 'models/bracket.bgcode')).toBe(
      'local:models/bracket.bgcode',
    );
  });

  // A path may hold colons of its own; a storage name never does.
  it('reads back the storage and the path it was made from', () => {
    const ref = fileRefOf('usb', 'a:b/c.gcode');

    expect(parseFileRef(ref)).toEqual({ storage: 'usb', path: 'a:b/c.gcode' });
  });

  it('is no reference without a storage, a path or a colon', () => {
    expect(parseFileRef('bracket.bgcode')).toBeUndefined();
    expect(parseFileRef(':bracket.bgcode')).toBeUndefined();
    expect(parseFileRef('local:')).toBeUndefined();
    expect(parseFileRef('')).toBeUndefined();
  });
});

describe('a printer file reference', () => {
  it('reads back the printer and the path it was made from', () => {
    const ref = printerFileRefOf(
      '2a5ccf9e-0169-4e1c-ac38-bb4810eac11f',
      'usb/a:b/part.bgcode',
    );

    expect(ref).toBe(
      '2a5ccf9e-0169-4e1c-ac38-bb4810eac11f:usb/a:b/part.bgcode',
    );
    expect(parsePrinterFileRef(ref)).toEqual({
      deviceId: '2a5ccf9e-0169-4e1c-ac38-bb4810eac11f',
      path: 'usb/a:b/part.bgcode',
    });
  });

  it('is no reference without a printer or a path', () => {
    expect(parsePrinterFileRef('usb/part.bgcode')).toBeUndefined();
    expect(parsePrinterFileRef('printer-1:')).toBeUndefined();
  });
});
