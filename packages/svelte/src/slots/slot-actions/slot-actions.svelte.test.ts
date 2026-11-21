import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import { t } from '../../i18n';
import { toast } from '../../primitives';
import { readyLoader } from '../../test/fakes';
import type { ModuleLoader } from '../loader.svelte';
import { SlotRegistry } from '../registry.svelte';
import type { ModuleContributes } from '../types';
import SlotActions from './slot-actions.svelte';

const registryFor = (block: ModuleContributes) => {
  const registry = new SlotRegistry();
  registry.declare('spools', block);

  return registry;
};

describe('SlotActions', () => {
  // ADR-20261027-wvj rule 17.
  it('offers an entry naming file types only on a file of one of them', async () => {
    const registry = registryFor({
      file_action: [
        {
          id: 'slice',
          title: 'Slice',
          destructive: false,
          file_types: ['.3mf'],
        },
        { id: 'tag', title: 'Tag', destructive: false },
      ],
    });

    await render(SlotActions, {
      props: {
        name: 'file-action',
        fileRef: 'local:part.gcode',
        label: 'part.gcode',
        registry,
      },
    });

    await page.getByRole('button', { name: /part\.gcode/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Tag' }))
      .toBeVisible();
    expect(page.getByRole('menuitem', { name: 'Slice' }).query()).toBeNull();
  });

  // ADR-20261110-5np.
  it('offers an entry with a devices selector only on a resident device', async () => {
    const registry = registryFor({
      device_action: [
        {
          id: 'wake',
          title: 'Wake display',
          destructive: false,
          devices: { resident: true },
        },
        { id: 'log', title: 'Log', destructive: false },
      ],
    });

    const other = await render(SlotActions, {
      props: {
        name: 'device-action',
        deviceId: 'mk4',
        device: {},
        label: 'MK4',
        registry,
      },
    });

    await page.getByRole('button', { name: /MK4/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Log' }))
      .toBeVisible();
    expect(
      page.getByRole('menuitem', { name: 'Wake display' }).query(),
    ).toBeNull();
    await other.unmount();

    await render(SlotActions, {
      props: {
        name: 'device-action',
        deviceId: 'host',
        device: { resident: true },
        label: 'Host',
        registry,
      },
    });

    await page.getByRole('button', { name: /Host/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Wake display' }))
      .toBeVisible();
  });

  it('offers no entry with a devices selector while the device has no record', async () => {
    const registry = registryFor({
      device_action: [
        {
          id: 'wake',
          title: 'Wake',
          destructive: false,
          devices: { resident: true },
        },
        { id: 'log', title: 'Log', destructive: false },
      ],
    });

    await render(SlotActions, {
      props: {
        name: 'device-action',
        deviceId: 'host',
        label: 'Host',
        registry,
      },
    });

    await page.getByRole('button', { name: /Host/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Log' }))
      .toBeVisible();
    expect(page.getByRole('menuitem', { name: 'Wake' }).query()).toBeNull();
  });

  // The page may show another subject before the bundle has loaded. The run
  // still acts on the one the person chose the entry for.
  const heldLoader = (): { loader: ModuleLoader; load: () => void } => {
    let load = (): void => undefined;
    const loading = new Promise<void>((resolve) => {
      load = resolve;
    });

    return {
      loader: {
        ready: false,
        activate: () => loading,
      } as unknown as ModuleLoader,
      load: () => {
        load();
      },
    };
  };

  it('runs a device entry for the device it was chosen for', async () => {
    const registry = registryFor({
      device_action: [{ id: 'wake', title: 'Wake', destructive: false }],
    });
    const run = vi.fn(() => Promise.resolve());
    registry.registerAction('spools', 'device-action', 'wake', run);
    const { loader, load } = heldLoader();

    const { rerender } = await render(SlotActions, {
      props: {
        name: 'device-action',
        deviceId: 'host',
        label: 'Host',
        registry,
        loader,
      },
    });
    await page.getByRole('button', { name: /Host/ }).click();
    await page.getByRole('menuitem', { name: 'Wake' }).click();
    await rerender({
      name: 'device-action',
      deviceId: 'mk4',
      label: 'MK4',
      registry,
      loader,
    });
    load();

    await expect.poll(() => run.mock.calls).toEqual([['host']]);
  });

  it('runs a file entry for the file it was chosen for', async () => {
    const registry = registryFor({
      file_action: [{ id: 'later', title: 'Print later', destructive: false }],
    });
    const run = vi.fn(() => Promise.resolve());
    registry.registerAction('spools', 'file-action', 'later', run);
    const { loader, load } = heldLoader();

    const { rerender } = await render(SlotActions, {
      props: {
        name: 'file-action',
        fileRef: 'local:a.gcode',
        label: 'a.gcode',
        registry,
        loader,
      },
    });
    await page.getByRole('button', { name: /a.gcode/ }).click();
    await page.getByRole('menuitem', { name: 'Print later' }).click();
    await rerender({
      name: 'file-action',
      fileRef: 'local:b.gcode',
      label: 'b.gcode',
      registry,
      loader,
    });
    load();

    await expect.poll(() => run.mock.calls).toEqual([['local:a.gcode']]);
  });

  it('draws nothing when no manifest declares an action', async () => {
    await render(SlotActions, {
      props: {
        name: 'device-action',
        deviceId: 'p',
        label: 'MK4',
        registry: new SlotRegistry(),
      },
    });

    expect(document.querySelector('[data-slot="device-action"]')).toBeNull();
    expect(page.getByRole('button', { name: /MK4/ }).query()).toBeNull();
  });

  it('runs the action with the device id and disables it while it runs', async () => {
    const registry = registryFor({
      device_action: [{ id: 'flush', title: 'Flush', destructive: false }],
    });
    let finish: (() => void) | undefined;
    const run = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    registry.registerAction('spools', 'device-action', 'flush', run);

    await render(SlotActions, {
      props: { name: 'device-action', deviceId: 'p', label: 'MK4', registry },
    });

    await page.getByRole('button', { name: /MK4/ }).click();
    await page.getByRole('menuitem', { name: 'Flush' }).click();
    expect(run).toHaveBeenCalledWith('p');

    await page.getByRole('button', { name: /MK4/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Flush' }))
      .toHaveAttribute('data-disabled');

    finish?.();
    await expect
      .element(page.getByRole('menuitem', { name: 'Flush' }))
      .not.toHaveAttribute('data-disabled');
  });

  it('shows a toast when run rejects, and when it throws without a promise', async () => {
    const error = vi.spyOn(toast, 'error').mockReturnValue('');
    const registry = registryFor({
      device_action: [
        { id: 'a', title: 'A', destructive: false },
        { id: 'b', title: 'B', destructive: false },
      ],
    });
    registry.registerAction('spools', 'device-action', 'a', () =>
      Promise.reject(new Error('nozzle is hot')),
    );
    registry.registerAction('spools', 'device-action', 'b', () => {
      throw new Error('no filament');
    });

    await render(SlotActions, {
      props: { name: 'device-action', deviceId: 'p', label: 'MK4', registry },
    });

    await page.getByRole('button', { name: /MK4/ }).click();
    await page.getByRole('menuitem', { name: 'A' }).click();
    await expect
      .poll(() => error.mock.calls.at(-1))
      .toEqual(['A', { description: 'nozzle is hot' }]);

    await page.getByRole('button', { name: /MK4/ }).click();
    await page.getByRole('menuitem', { name: 'B' }).click();
    await expect
      .poll(() => error.mock.calls.at(-1))
      .toEqual(['B', { description: 'no filament' }]);
  });

  it('names the module when a declared action has no run after the loader is ready', async () => {
    const error = vi.spyOn(toast, 'error').mockReturnValue('');
    const registry = registryFor({
      device_action: [{ id: 'a', title: 'A', destructive: false }],
    });

    await render(SlotActions, {
      props: {
        name: 'device-action',
        deviceId: 'p',
        label: 'MK4',
        registry,
        loader: readyLoader(),
      },
    });

    await page.getByRole('button', { name: /MK4/ }).click();
    await page.getByRole('menuitem', { name: 'A' }).click();
    await expect
      .poll(() => error.mock.calls.at(-1)?.[0])
      .toBe(t('slotActions.missing', { module: 'spools' }));
  });

  it('draws a destructive action as destructive', async () => {
    const registry = registryFor({
      device_action: [{ id: 'flush', title: 'Flush', destructive: true }],
    });

    await render(SlotActions, {
      props: { name: 'device-action', deviceId: 'p', label: 'MK4', registry },
    });

    await page.getByRole('button', { name: /MK4/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Flush' }))
      .toHaveAttribute('data-variant', 'destructive');
  });

  it('runs a file action with the file reference', async () => {
    const registry = registryFor({
      file_action: [{ id: 'later', title: 'Print later', destructive: false }],
    });
    const run = vi.fn(() => Promise.resolve());
    registry.registerAction('spools', 'file-action', 'later', run);

    await render(SlotActions, {
      props: {
        name: 'file-action',
        fileRef: 'local:models/bracket.bgcode',
        label: 'bracket.bgcode',
        registry,
      },
    });

    await page.getByRole('button', { name: /bracket.bgcode/ }).click();
    await page.getByRole('menuitem', { name: 'Print later' }).click();
    expect(run).toHaveBeenCalledWith('local:models/bracket.bgcode');
  });

  it('runs a printer file action with the printer file reference, for its file types', async () => {
    const registry = registryFor({
      device_file_action: [
        {
          id: 'queue',
          title: 'Add to queue',
          destructive: false,
          file_types: ['.bgcode'],
        },
        {
          id: 'slice',
          title: 'Slice',
          destructive: false,
          file_types: ['.3mf'],
        },
      ],
    });
    const run = vi.fn(() => Promise.resolve());
    registry.registerAction('spools', 'device-file-action', 'queue', run);

    await render(SlotActions, {
      props: {
        name: 'device-file-action',
        printerFileRef: 'printer-1:usb/bracket.bgcode',
        label: 'bracket.bgcode',
        registry,
      },
    });

    await page.getByRole('button', { name: /bracket.bgcode/ }).click();
    expect(page.getByRole('menuitem', { name: 'Slice' }).query()).toBeNull();
    await page.getByRole('menuitem', { name: 'Add to queue' }).click();
    expect(run).toHaveBeenCalledWith('printer-1:usb/bracket.bgcode');
  });

  // A screen with a menu of its own hands its items in, so the person sees
  // one menu and not two.
  it('draws the screen own items in the same menu, with or without an entry', async () => {
    const own = createRawSnippet(() => ({
      render: () => '<div role="menuitem">Download</div>',
    }));
    const registry = registryFor({
      file_action: [{ id: 'later', title: 'Print later', destructive: false }],
    });

    const empty = await render(SlotActions, {
      props: {
        name: 'file-action',
        fileRef: 'local:a.bgcode',
        label: 'a.bgcode',
        items: own,
        registry: new SlotRegistry(),
      },
    });
    await page.getByRole('button', { name: /a.bgcode/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Download' }))
      .toBeInTheDocument();
    await empty.unmount();

    await render(SlotActions, {
      props: {
        name: 'file-action',
        fileRef: 'local:a.bgcode',
        label: 'a.bgcode',
        items: own,
        registry,
      },
    });
    await page.getByRole('button', { name: /a.bgcode/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Download' }))
      .toBeInTheDocument();
    await expect
      .element(page.getByRole('menuitem', { name: 'Print later' }))
      .toBeInTheDocument();
  });
  // ADR-20261029-dgv: a primary entry is a button on the file's own page and
  // a menu item everywhere else.
  it('draws a primary entry as a button only where the page asks for buttons', async () => {
    const registry = registryFor({
      file_action: [
        { id: 'slice', title: 'Slice', destructive: false, primary: true },
        { id: 'tag', title: 'Tag', destructive: false },
      ],
    });
    const run = vi.fn(() => Promise.resolve());
    registry.registerAction('spools', 'file-action', 'slice', run);

    const listing = await render(SlotActions, {
      props: {
        name: 'file-action',
        fileRef: 'local:part.3mf',
        label: 'part.3mf',
        registry,
      },
    });
    expect(page.getByRole('button', { name: 'Slice' }).query()).toBeNull();
    await page.getByRole('button', { name: /part\.3mf/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Slice' }))
      .toBeVisible();
    await listing.unmount();

    await render(SlotActions, {
      props: {
        name: 'file-action',
        fileRef: 'local:part.3mf',
        label: 'part.3mf',
        buttons: true,
        registry,
      },
    });
    await page.getByRole('button', { name: 'Slice' }).click();
    expect(run).toHaveBeenCalledWith('local:part.3mf');

    await page.getByRole('button', { name: /part\.3mf/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Tag' }))
      .toBeVisible();
    expect(page.getByRole('menuitem', { name: 'Slice' }).query()).toBeNull();
  });
});
