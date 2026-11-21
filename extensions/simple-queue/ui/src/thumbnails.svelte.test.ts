import { describe, expect, it } from 'vitest';

import { holdThumbnails, thumbnailOf } from './thumbnails';

describe('the picture of a queued file', () => {
  it('has no address until the module is set up, so a view draws the glyph', () => {
    expect(
      thumbnailOf('mk4-1', { storage: 'local', path: 'a.bgcode' }),
    ).toBeUndefined();
  });

  it('is read on the server for a file in a storage, and from the printer for a file only there', () => {
    holdThumbnails({ url: 'http://server' } as Parameters<
      typeof holdThumbnails
    >[0]);

    expect(
      thumbnailOf('mk4-1', { storage: 'local', path: 'boxes/lid 1.bgcode' }),
    ).toBe(
      'http://server/api/v1/storages/local/thumbnail?path=boxes%2Flid%201.bgcode',
    );

    const onPrinter = thumbnailOf('mk4-1', {
      storage: '',
      path: 'usb/lid.bgcode',
    });
    expect(onPrinter).toContain('/api/v1/devices/mk4-1/files/thumbnail');
    expect(onPrinter).toContain('path=usb%2Flid.bgcode');
  });

  it('asks again for a printer file once its size is known', () => {
    holdThumbnails({ url: 'http://server' } as Parameters<
      typeof holdThumbnails
    >[0]);
    const landing = { storage: '', path: 'usb/lid.bgcode' };

    // Asked for while the file was landing, the picture may be refused, and a
    // view keeps a refused address. The size the queue learns after names
    // another address, which is asked for again.
    expect(thumbnailOf('mk4-1', { ...landing, size: 3_023_989 })).not.toBe(
      thumbnailOf('mk4-1', landing),
    );
    expect(thumbnailOf('mk4-1', { ...landing, size: 3_023_989 })).toContain(
      'size=3023989',
    );
  });

  it('names the version of a printer file by its size and its time', () => {
    holdThumbnails({ url: 'http://server' } as Parameters<
      typeof holdThumbnails
    >[0]);
    const file = { storage: '', path: 'usb/lid.bgcode', size: 3_023_989 };

    // Without the time the server cannot find the picture it holds, and asks
    // the printer for it again.
    expect(
      thumbnailOf('mk4-1', { ...file, modified: 1_794_000_000 }),
    ).toContain('size=3023989&modified=1794000000');
    expect(
      thumbnailOf('mk4-1', { ...file, modified: '2026-11-07T07:20:00Z' }),
    ).toContain('modified=1794036000');
  });
});
