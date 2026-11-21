import { describe, expect, it } from 'vitest';

import { modulePageHref } from '../module-href';
import { withModuleNav } from './navigation';
import PlainPage from '../test/plain-page.svelte';
import PlainPane from '../test/plain-pane.svelte';
import PlainSection from '../test/plain-section.svelte';
import PrintSectionProbe from '../test/print-section-probe.svelte';
import {
  describeRefusal,
  SlotRegistry,
  UndeclaredRegistration,
} from './registry.svelte';
import type { ModuleContributes } from './types';

const spools: ModuleContributes = {
  pages: [
    { path: 'spools', title: 'Spools', icon: 'layers', hidden: false },
    { path: 'debug', title: 'Debug', hidden: true },
  ],
  device_card: [{ id: 'spool-status', title: 'Loaded spool' }],
  settings: [{ id: 'spools', title: 'Spools' }],
};

const registryFor = (contributes = spools) => {
  const registry = new SlotRegistry();
  registry.declare('spools', contributes);

  return registry;
};

describe('SlotRegistry', () => {
  it('takes a page the manifest declared, with the manifest title and icon', () => {
    const registry = registryFor();
    registry.registerPage('spools', 'spools', PlainPage);

    expect(registry.pages()).toHaveLength(1);
    expect(registry.pages()[0]?.title).toBe('Spools');
    expect(registry.pages()[0]?.icon).toBe('layers');
  });

  // ADR-20260911-4p6 rule 21: the manifest is the ceiling.
  it('refuses a page the manifest does not declare, and records it', () => {
    const registry = registryFor();

    expect(() => {
      registry.registerPage('spools', 'secrets', PlainPage);
    }).toThrow(UndeclaredRegistration);
    expect(registry.pages()).toHaveLength(0);
    expect(registry.refusals()).toEqual([
      {
        namespace: 'spools',
        reason: 'undeclared',
        slot: 'page',
        name: 'secrets',
      },
    ]);
  });

  it('refuses a device-card section the manifest does not declare', () => {
    const registry = registryFor();

    expect(() => {
      registry.registerSection(
        'spools',
        'device-card',
        'spool-history',
        PlainSection,
      );
    }).toThrow(UndeclaredRegistration);
    expect(registry.sections('device-card')).toHaveLength(0);
    expect(registry.refusals()[0]?.reason).toBe('undeclared');
  });

  it('refuses a settings pane the manifest does not declare', () => {
    const registry = registryFor();

    expect(() => {
      registry.registerSection('spools', 'settings', 'colours', PlainPane);
    }).toThrow(UndeclaredRegistration);
    expect(registry.sections('settings')).toHaveLength(0);
  });

  it('lists the declared sections of a slot before any bundle registers', () => {
    const registry = registryFor();

    expect(registry.declaredSections('device-card')).toEqual([
      { namespace: 'spools', id: 'spool-status', title: 'Loaded spool' },
    ]);
  });

  // ADR-20261110-5np.
  it('carries the devices selector from the manifest to the declared and registered entries', () => {
    const registry = new SlotRegistry();
    registry.declare('display', {
      device_tab: [
        { id: 'display', title: 'Display', devices: { resident: true } },
      ],
      device_action: [
        {
          id: 'wake',
          title: 'Wake',
          destructive: false,
          devices: { resident: true },
        },
      ],
    });
    registry.registerSection('display', 'device-tab', 'display', PlainSection);
    registry.registerAction('display', 'device-action', 'wake', () =>
      Promise.resolve(),
    );

    const resident = { resident: true };
    expect(registry.declaredSections('device-tab')[0]?.devices).toEqual(
      resident,
    );
    expect(registry.declaredActions('device-action')[0]?.devices).toEqual(
      resident,
    );
    expect(registry.sections('device-tab')[0]?.devices).toEqual(resident);
    expect(registry.actions('device-action')[0]?.devices).toEqual(resident);

    registry.refuse({ namespace: 'display', reason: 'threw', detail: 'boom' });
    expect(registry.refusedSections('device-tab')[0]?.devices).toEqual(
      resident,
    );
  });

  it('carries cross_origin_isolated from the manifest to the declared tabs', () => {
    const registry = new SlotRegistry();
    registry.declare('console', {
      device_tab: [
        { id: 'console', title: 'Device page', cross_origin_isolated: true },
        { id: 'log', title: 'Log' },
      ],
    });

    expect(
      registry
        .declaredSections('device-tab')
        .map((entry) => [entry.id, entry.crossOriginIsolated]),
    ).toEqual([
      ['console', true],
      ['log', undefined],
    ]);

    registry.refuse({ namespace: 'console', reason: 'threw', detail: 'boom' });
    expect(registry.refusedSections('device-tab')[0]?.crossOriginIsolated).toBe(
      true,
    );
  });

  // Controller ruling: a module the loader refused shows no tab and no
  // action menu item, so the tab strip and the kebab menu must not draw from
  // a namespace's declaration once it has a refusal against it.
  it('drops a refused namespace from the declared sections of a slot', () => {
    const registry = new SlotRegistry();
    registry.declare('spools', {
      device_tab: [{ id: 'nozzle', title: 'Nozzle' }],
    });
    registry.declare('broken', { device_tab: [{ id: 'x', title: 'X' }] });
    registry.refuse({ namespace: 'broken', reason: 'threw', detail: 'boom' });

    expect(registry.declaredSections('device-tab')).toEqual([
      { namespace: 'spools', id: 'nozzle', title: 'Nozzle' },
    ]);
  });

  it('drops a namespace the loader refused for its library range from the declared actions of a slot', () => {
    const registry = new SlotRegistry();
    registry.declare('spools', {
      device_action: [
        { id: 'mark', title: 'Mark changed', destructive: false },
      ],
    });
    registry.refuse({
      namespace: 'spools',
      reason: 'library-mismatch',
      range: '>=0.2.0 <1.0.0',
      version: '0.1.0',
    });

    expect(registry.declaredActions('device-action')).toEqual([]);
  });

  it('refuses everything from a namespace that declared nothing', () => {
    const registry = new SlotRegistry();

    expect(() => {
      registry.registerPage('nobody', 'spools', PlainPage);
    }).toThrow(UndeclaredRegistration);
  });

  // ADR-20260911-4p6 rule 10.
  it('adds no sidebar item for a hidden page, and its route still works', () => {
    const registry = registryFor();
    registry.registerPage('spools', 'debug', PlainPage);

    expect(registry.pages()).toHaveLength(1);
    expect(registry.sidebarItems()).toHaveLength(0);
  });

  describe('operator-hidden contributions', () => {
    const hiddenRegistry = (hidden: string[]) => {
      const registry = new SlotRegistry();
      registry.declare('spools', spools, hidden);

      return registry;
    };

    it('accepts a hidden card but returns it from no query', () => {
      const registry = hiddenRegistry(['device_card/spool-status']);

      registry.registerSection(
        'spools',
        'device-card',
        'spool-status',
        PlainSection,
      );

      expect(registry.sections('device-card')).toEqual([]);
      expect(registry.declaredSections('device-card')).toEqual([]);
      expect(registry.refusals()).toEqual([]);
    });

    it('returns a hidden file action and print section from no query', () => {
      const registry = new SlotRegistry();
      registry.declare(
        'planner',
        {
          file_action: [
            { id: 'plan', title: 'Plan a print', destructive: false },
          ],
          print_section: [{ id: 'planner', title: 'Planner' }],
        },
        ['file_action/plan', 'print_section/planner'],
      );

      registry.registerAction('planner', 'file-action', 'plan', () =>
        Promise.resolve(),
      );
      registry.registerSection(
        'planner',
        'print-section',
        'planner',
        PrintSectionProbe,
      );

      expect(registry.actions('file-action')).toEqual([]);
      expect(registry.declaredActions('file-action')).toEqual([]);
      expect(registry.sections('print-section')).toEqual([]);
      expect(registry.declaredSections('print-section')).toEqual([]);
      expect(registry.refusals()).toEqual([]);
    });

    it('drops a hidden page from the sidebar, and its route still works', () => {
      const registry = hiddenRegistry(['pages/spools']);
      registry.registerPage('spools', 'spools', PlainPage);

      expect(registry.pages()).toHaveLength(1);
      expect(registry.sidebarItems()).toEqual([]);
    });

    it('applies a list replaced after the module registered', () => {
      const registry = hiddenRegistry([]);
      registry.registerSection(
        'spools',
        'device-card',
        'spool-status',
        PlainSection,
      );

      registry.hide('spools', ['device_card/spool-status']);
      expect(registry.sections('device-card')).toEqual([]);

      registry.hide('spools', []);
      expect(registry.sections('device-card')).toHaveLength(1);
    });

    it('keeps what is not listed, and ignores a stale entry', () => {
      const registry = hiddenRegistry(['pages/gone', 'device_card/spools']);
      registry.registerPage('spools', 'spools', PlainPage);
      registry.registerSection(
        'spools',
        'device-card',
        'spool-status',
        PlainSection,
      );

      expect(registry.pages()).toHaveLength(1);
      expect(registry.sections('device-card')).toHaveLength(1);
    });
  });

  // ADR-20260911-4p6 rules 9 and 15.
  it('builds the href itself and sorts the core first', () => {
    const registry = registryFor();
    registry.registerPage('spools', 'spools', PlainPage);

    const core = [{ id: 'printers', label: 'Fleet', href: '/' }];
    const items = withModuleNav(core, registry.sidebarItems());

    expect(items.map((item) => item.id)).toEqual([
      'printers',
      'module:spools/spools',
    ]);
    expect(items[1]?.href).toBe('/m/spools/spools');
    expect(modulePageHref('spools', 'spools')).toBe('/m/spools/spools');
  });

  it('forgets one module whole and keeps the refusals', () => {
    const registry = registryFor();
    registry.registerPage('spools', 'spools', PlainPage);
    registry.registerSection('spools', 'settings', 'spools', PlainPane);
    registry.refuse({ namespace: 'spools', reason: 'threw', detail: 'boom' });

    registry.forget('spools');

    expect(registry.pages()).toHaveLength(0);
    expect(registry.sections('settings')).toHaveLength(0);
    expect(registry.refusals()).toHaveLength(1);
  });
});

describe('files a module asks to send to a printer', () => {
  const request = (deviceId: string, action: string) => ({
    namespace: 'queue',
    action,
    deviceId,
    files: [],
  });

  it('drops a request for a printer whose page is not open', () => {
    const registry = new SlotRegistry();

    expect(registry.requestUpload(request('a', 'first'))).toBe(false);
    registry.openUploads('a');

    expect(registry.uploadFor('a')).toBeUndefined();
  });

  it('holds every request for an open page, oldest first', () => {
    const registry = new SlotRegistry();
    registry.openUploads('a');

    expect(registry.requestUpload(request('a', 'first'))).toBe(true);
    expect(registry.requestUpload(request('a', 'second'))).toBe(true);

    expect(registry.takeUpload('a')?.action).toBe('first');
    expect(registry.takeUpload('a')?.action).toBe('second');
    expect(registry.takeUpload('a')).toBeUndefined();
  });

  it('drops what waits when the page closes', () => {
    const registry = new SlotRegistry();
    const close = registry.openUploads('a');
    registry.requestUpload(request('a', 'first'));

    close();
    registry.openUploads('a');

    expect(registry.uploadFor('a')).toBeUndefined();
  });
});

describe('describeRefusal', () => {
  it('says why, in one line a settings screen can show', () => {
    expect(
      describeRefusal({
        namespace: 'spools',
        reason: 'library-mismatch',
        range: '>=0.3.0 <1.0.0',
        version: '0.1.0',
      }),
    ).toBe('It needs the component library >=0.3.0 <1.0.0, and this is 0.1.0.');
    expect(
      describeRefusal({
        namespace: 'spools',
        reason: 'threw',
        detail: 'boom',
      }),
    ).toBe('It failed while loading: boom');
  });
});
