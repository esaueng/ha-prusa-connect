import { describe, expect, it, vi } from 'vitest';

import { addPrinterFile, addUpload } from './device-file-action';
import { fakeApi, t, waitingQueue } from './test/fakes';

const host = {
  t,
  fleet: { device: (id: string) => ({ id, name: 'MK4 by the window' }) },
} as unknown as Parameters<typeof addPrinterFile>[0];

describe('Add to queue in the menu of a file on the printer', () => {
  it('adds the file where it lies, naming no storage', async () => {
    const addEntry = vi.fn(() =>
      Promise.resolve({
        ...waitingQueue,
        entries: [
          ...waitingQueue.entries,
          {
            ...waitingQueue.entries[0],
            id: 'p',
            storage: '',
            path: 'usb/gear.bgcode',
          },
        ],
      } as typeof waitingQueue),
    );

    await addPrinterFile(
      host,
      { ...fakeApi(), addEntry },
      'mk4-1:usb/gear.bgcode',
    );

    expect(addEntry).toHaveBeenCalledWith({
      path: { device: 'mk4-1' },
      body: { path: 'usb/gear.bgcode' },
    });
  });

  it('throws the reason the queue refused it', async () => {
    const addEntry = vi.fn(() =>
      Promise.reject(
        Object.assign(new Error('full'), {
          status: 409,
          title: 'The queue of this printer is full',
        }),
      ),
    );

    await expect(
      addPrinterFile(host, { ...fakeApi(), addEntry }, 'mk4-1:usb/gear.bgcode'),
    ).rejects.toThrow();
  });

  it('refuses a reference that names no printer file', async () => {
    await expect(addPrinterFile(host, fakeApi(), 'nonsense')).rejects.toThrow(
      'This is not a file on a printer.',
    );
  });
});

describe('Add to queue for a file dropped on a printer', () => {
  it('adds the file as one still on its way to the printer', async () => {
    const addEntry = vi.fn(() =>
      Promise.resolve({
        ...waitingQueue,
        entries: [
          ...waitingQueue.entries,
          {
            ...waitingQueue.entries[0],
            id: 'd',
            storage: '',
            path: 'usb/gear.bgcode',
          },
        ],
      } as typeof waitingQueue),
    );

    await addUpload(host, { ...fakeApi(), addEntry }, 'mk4-1:usb/gear.bgcode');

    expect(addEntry).toHaveBeenCalledWith({
      path: { device: 'mk4-1' },
      body: { path: 'usb/gear.bgcode', arriving: true },
    });
  });

  it('refuses a reference that names no printer file', async () => {
    await expect(addUpload(host, fakeApi(), 'nonsense')).rejects.toThrow(
      t('simple-queue.fileAction.absent'),
    );
  });
});
