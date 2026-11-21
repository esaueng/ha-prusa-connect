import { describe, expect, it } from 'vitest';

import { moduleTabs } from './module-tabs';
import { SlotRegistry } from './registry.svelte';

describe('module tabs', () => {
  it('routes a tab under the namespace and the id, split at the last dot', () => {
    const registry = new SlotRegistry();
    registry.declare('com.github.aux.acme.nozzle-watch', {
      device_tab: [{ id: 'nozzle', title: 'Nozzle' }],
    });
    registry.declare('com.example.other', {
      device_tab: [{ id: 'nozzle', title: 'Other nozzle' }],
    });

    expect(moduleTabs(registry, 'device-tab')).toEqual([
      {
        id: 'com.github.aux.acme.nozzle-watch.nozzle',
        label: 'Nozzle',
        isolated: false,
      },
      {
        id: 'com.example.other.nozzle',
        label: 'Other nozzle',
        isolated: false,
      },
    ]);
  });

  it('takes the slot as a union, as a shared tab strip passes it', () => {
    const registry = new SlotRegistry();
    registry.declare('com.example.view', {
      device_tab: [
        { id: 'display', title: 'Display', devices: { resident: true } },
      ],
      file_tab: [{ id: 'gcode', title: 'G-code', file_types: ['.gcode'] }],
    });
    const tabsOf = (
      slot: 'device-tab' | 'file-tab',
      fileRef?: string,
    ): readonly { id: string; label: string }[] =>
      moduleTabs(registry, slot, fileRef);

    expect(tabsOf('file-tab', 'local:a.gcode')).toEqual([
      { id: 'com.example.view.gcode', label: 'G-code', isolated: false },
    ]);
    // A file reference is no device record, so a tab with a devices selector
    // is not kept.
    expect(tabsOf('device-tab', 'local:a.gcode')).toEqual([]);
    // A device record is no file reference: the types refuse it on a file
    // tab, and at run time it reads as no file, so every file tab is kept.
    // @ts-expect-error -- a file tab takes a file reference, not a record
    expect(moduleTabs(registry, 'file-tab', { resident: true })).toHaveLength(
      1,
    );
  });

  it('says which tab asks for a cross-origin isolated page', () => {
    const registry = new SlotRegistry();
    registry.declare('com.example.console', {
      device_tab: [
        { id: 'console', title: 'Device page', cross_origin_isolated: true },
        { id: 'log', title: 'Log' },
      ],
    });

    expect(
      moduleTabs(registry, 'device-tab').map((tab) => [tab.id, tab.isolated]),
    ).toEqual([
      ['com.example.console.console', true],
      ['com.example.console.log', false],
    ]);
  });

  // ADR-20261110-5np.
  describe('a device tab with a devices selector', () => {
    const registry = new SlotRegistry();
    registry.declare('com.example.console', {
      device_tab: [
        { id: 'console', title: 'Device page', devices: { resident: true } },
        { id: 'log', title: 'Log' },
      ],
    });
    const devicePage = {
      id: 'com.example.console.console',
      label: 'Device page',
      isolated: false,
    };
    const log = {
      id: 'com.example.console.log',
      label: 'Log',
      isolated: false,
    };

    it('is a tab of the printer the server runs on', () => {
      expect(moduleTabs(registry, 'device-tab', { resident: true })).toEqual([
        devicePage,
        log,
      ]);
    });

    it('is no tab of any other printer', () => {
      expect(moduleTabs(registry, 'device-tab', {})).toEqual([log]);
      expect(moduleTabs(registry, 'device-tab', { resident: false })).toEqual([
        log,
      ]);
    });

    it('is no tab while the printer has no record yet', () => {
      expect(moduleTabs(registry, 'device-tab')).toEqual([log]);
      expect(moduleTabs(registry, 'device-tab', undefined)).toEqual([log]);
    });
  });
});
