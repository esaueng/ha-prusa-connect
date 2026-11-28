import type { ModuleHost } from '@prusa/connect-ui/slots';
import { describe, expect, it, vi } from 'vitest';

import { PageClosed, PlateChanged } from '../../plate/sendable';
import { prepareOn } from './prepare-api';

type Storages = Awaited<ReturnType<ModuleHost['client']['listDeviceStorages']>>;

const usb: Storages = {
  reported: true,
  items: [
    {
      name: 'usb',
      mountpoint: '/usb',
      read_only: false,
      type: 'USB',
      free_space: 1_000_000,
    },
  ],
};

const started = {
  command: { command_id: 'c1', ok: true },
  path: 'usb/plate.gcode',
};

/** A client that answers the printer's storages when the test says so. */
const clientWith = () => {
  let answer: (storages: Storages) => void = () => undefined;
  const listDeviceStorages = vi.fn(
    () =>
      new Promise<Storages>((resolve) => {
        answer = resolve;
      }),
  );
  const printDeviceFile = vi.fn(() => Promise.resolve(started));
  const client = { listDeviceStorages, printDeviceFile };
  return {
    // The print path calls these two operations of the client and no other.
    client: client as unknown as ModuleHost['client'],
    calls: client,
    answer: (storages: Storages) => {
      answer(storages);
    },
  };
};

/** The module's own routes, which printing never calls. */
const unused = {
  submitJob: () => Promise.reject(new Error('printing submits no job')),
  readJob: () => Promise.reject(new Error('printing reads no job')),
};

const request = {
  deviceId: 'henry',
  storage: 'local',
  source: 'sliced/plate.gcode',
  name: 'plate.gcode',
};

describe('printing the sliced plate', () => {
  it('asks the printer where the file lands, then starts it there', async () => {
    const { client, calls, answer } = clientWith();
    const printed = prepareOn(client, unused).print(
      request,
      new AbortController().signal,
      () => undefined,
    );
    answer(usb);
    await printed;
    expect(calls.listDeviceStorages).toHaveBeenCalledWith(
      { path: { device_id: 'henry' }, query: { refresh: true } },
      expect.any(AbortSignal),
    );
    expect(calls.printDeviceFile).toHaveBeenCalledWith({
      path: { device_id: 'henry' },
      body: {
        path: 'usb/plate.gcode',
        source: 'sliced/plate.gcode',
        storage: 'local',
      },
    });
  });

  it('starts nothing when the workspace closed while the printer was asked', async () => {
    const { client, calls, answer } = clientWith();
    const controller = new AbortController();
    const printed = prepareOn(client, unused).print(
      request,
      controller.signal,
      () => {
        if (controller.signal.aborted) throw new PageClosed();
      },
    );
    controller.abort();
    answer(usb);
    await expect(printed).rejects.toBeInstanceOf(PageClosed);
    expect(calls.printDeviceFile).not.toHaveBeenCalled();
  });

  it('starts nothing when the plate stopped being sendable while the printer was asked', async () => {
    const { client, calls, answer } = clientWith();
    let sendable = true;
    const printed = prepareOn(client, unused).print(
      request,
      new AbortController().signal,
      () => {
        if (!sendable) throw new PlateChanged();
      },
    );
    // The bed shrank, or the printer started printing, during the lookup.
    sendable = false;
    answer(usb);
    await expect(printed).rejects.toBeInstanceOf(PlateChanged);
    expect(calls.printDeviceFile).not.toHaveBeenCalled();
  });
});
