// The printer page wiring: the commands a tab sends reach the printer the
// person confirmed them on, whichever printer the page shows afterwards.
import type { StreamEvent } from '@prusa/connect';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { page, userEvent } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import { appContext } from '../../test/app-context';
import { toast } from '@prusa/connect-ui/primitives';
import { SlotRegistry } from '@prusa/connect-ui/slots';

import {
  frame,
  json,
  problem,
  type Routes,
  type Stream,
  stream,
  stubFetch,
} from '../../test/stub';
// Not exported from the package: reaches for the same fixture the registry's
// own tests use, rather than keep a second copy in sync.
import PlainSection from '../../../../packages/svelte/src/test/plain-section.svelte';
import WithSlots from '../../test/with-slots.svelte';
import type * as Isolation from '../isolation';
import PrinterDetail from './printer-detail.svelte';

// Whether the browser can isolate a page is the test's to set: a test page on
// `localhost` always can.
const isolation = vi.hoisted(() => ({ capable: true }));
vi.mock('../isolation', async (original) => {
  const real = await original<typeof Isolation>();

  return {
    ...real,
    browserIsolation: (): Isolation.DocumentIsolation => ({
      ...real.browserIsolation(),
      capable: isolation.capable,
    }),
  };
});

const printer = (id: string) => ({
  id,
  name: `Printer ${id}`,
  kind: 'printer',
  model: 'MK4S',
  connected: true,
  labels: {},
  actions: ['observe', 'operate'],
  capabilities: ['core.command.delete_file', 'core.command.delete_folder'],
  state: { '/state': 'IDLE' },
});

const file = (name: string) => ({
  folder: false,
  name,
  path: `usb/${name}`,
  read_only: false,
  size: 1024,
});

// Every listing answers the same rows. The drive is the one folder in them,
// so the tab opens it and shows the three files beside it.
const listing = json(200, {
  items: [
    { folder: true, name: 'usb', path: 'usb', read_only: false, size: 0 },
    file('one.gcode'),
    file('two.gcode'),
    file('three.gcode'),
  ],
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('the printer page', () => {
  // The panel keeps the port it was given, but the port the page builds
  // must keep the printer too, or the rest of a delete follows the switch.
  it('sends the rest of a delete to the printer it was confirmed on', async () => {
    const answers: (() => void)[] = [];
    const routes: Routes = {
      'GET /api/v1/devices': json(200, {
        items: [printer('a'), printer('b')],
        total: 2,
      }),
      'GET /api/v1/devices/a/files': listing,
      'GET /api/v1/devices/b/files': listing,
      'POST /api/v1/devices/a/commands': () =>
        new Promise<Response>((resolve) => {
          answers.push(() => {
            resolve(json(200, { ok: true, command_id: 'c' }));
          });
        }),
      'POST /api/v1/devices/b/commands': json(200, {
        ok: true,
        command_id: 'c',
      }),
    };
    const fetcher = stubFetch(routes);
    vi.stubGlobal('fetch', fetcher.fetch);

    const { rerender } = await render(PrinterDetail, {
      props: {
        ctx: appContext(routes, { params: { id: 'a', tab: 'files' } }),
      },
    });

    for (const name of ['one.gcode', 'two.gcode', 'three.gcode']) {
      await page.getByRole('checkbox', { name: `Select ${name}` }).click();
    }
    await page.getByRole('button', { name: 'Delete', exact: true }).click();
    await page
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Delete', exact: true })
      .click();

    await vi.waitFor(() => {
      expect(answers).toHaveLength(1);
    });
    await rerender({
      ctx: appContext(routes, { params: { id: 'b', tab: 'files' } }),
    });

    for (let sent = 1; sent <= 3; sent += 1) {
      await vi.waitFor(() => {
        expect(answers.length).toBeGreaterThanOrEqual(sent);
      });
      answers[sent - 1]?.();
    }

    expect(fetcher.count('POST /api/v1/devices/a/commands')).toBe(3);
    expect(fetcher.count('POST /api/v1/devices/b/commands')).toBe(0);
  });
});

describe('the upload area on the printer page', () => {
  const uploads = json(200, {
    items: [
      {
        browse: true,
        display_name: 'Uploads',
        download_url: true,
        move: true,
        name: 'uploads',
        ranges: true,
        write: true,
      },
    ],
  });

  // A printer that does not answer, or whose queue is full, must not take the
  // upload area away without a word: that is what this page set out to stop.
  it.each([
    ['a full command queue', problem(409, 'core.printer/COMMAND_QUEUE_FULL')],
    ['a printer slow to answer', problem(504, 'core.printer/TIMEOUT')],
  ])('says so and offers to ask again after %s', async (_, refusal) => {
    const routes: Routes = {
      'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
      'GET /api/v1/devices/a/files': listing,
      'GET /api/v1/devices/a/storages': refusal,
      'GET /api/v1/storages': uploads,
    };
    vi.stubGlobal('fetch', stubFetch(routes).fetch);

    await render(PrinterDetail, {
      props: {
        ctx: appContext(routes, { params: { id: 'a', tab: 'status' } }),
      },
    });

    await expect
      .element(page.getByText("Waiting for the printer's storage"))
      .toBeVisible();
    await expect
      .element(page.getByRole('button', { name: 'Check again' }))
      .toBeVisible();
  });

  // A person who may only watch the printer cannot upload to it, and asking
  // again would be refused the same way.
  it('says nothing to a person the read was refused to', async () => {
    const routes: Routes = {
      'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
      'GET /api/v1/devices/a/files': listing,
      'GET /api/v1/devices/a/storages': problem(403, 'core.authz/FORBIDDEN'),
      'GET /api/v1/storages': uploads,
    };
    const fetcher = stubFetch(routes);
    vi.stubGlobal('fetch', fetcher.fetch);

    await render(PrinterDetail, {
      props: {
        ctx: appContext(routes, { params: { id: 'a', tab: 'status' } }),
      },
    });

    await vi.waitFor(() => {
      expect(fetcher.count('GET /api/v1/devices/a/storages')).toBe(1);
    });
    await expect
      .element(page.getByRole('button', { name: 'Check again' }))
      .not.toBeInTheDocument();
  });
});

describe('files a module sends to the printer page', () => {
  const uploads = json(200, {
    items: [
      {
        browse: true,
        display_name: 'Uploads',
        download_url: true,
        move: true,
        name: 'uploads',
        ranges: true,
        write: true,
      },
    ],
  });
  const drives = json(200, {
    items: [
      {
        free_space: 1024,
        mountpoint: '/usb',
        name: 'usb',
        read_only: false,
        type: 'USB',
      },
    ],
    reported: true,
  });
  const queueing = (): SlotRegistry => {
    const registry = new SlotRegistry();
    registry.declare('queue', {
      device_upload_action: [
        { id: 'queue', title: 'Add to queue', destructive: false },
      ],
    });

    return registry;
  };
  const stored = (name: string) => ({ storage: 'uploads', path: name });

  // The server takes one transfer for a printer at a time and refuses a
  // second with TRANSFER_BUSY, so a second file sent in the same moment would
  // never reach the printer or the module's choice.
  it('sends the stored files of one request one after another', async () => {
    const answers: (() => void)[] = [];
    const sent: string[] = [];
    const routes: Routes = {
      'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
      'GET /api/v1/devices/a/files': listing,
      'GET /api/v1/devices/a/storages': drives,
      'GET /api/v1/storages': uploads,
      'POST /api/v1/devices/a/files': (call) =>
        new Promise<Response>((resolve) => {
          const body = JSON.parse(call.body ?? '{}') as {
            source: string;
            path: string;
          };
          sent.push(body.source);
          answers.push(() => {
            resolve(
              json(200, {
                path: body.path,
                transfer: {
                  transfer_id: `t-${body.source}`,
                  device_id: 'a',
                  storage: 'uploads',
                  source: body.source,
                  path: body.path,
                  state: 'done',
                  bytes_sent: 1024,
                  bytes_total: 1024,
                  started_at: '2026-11-18T08:00:00Z',
                },
              }),
            );
          });
        }),
    };
    vi.stubGlobal('fetch', stubFetch(routes).fetch);
    const registry = queueing();

    await render(WithSlots, {
      props: {
        registry,
        screen: PrinterDetail,
        ctx: appContext(routes, { params: { id: 'a', tab: 'status' } }),
      },
    });
    registry.requestUpload({
      namespace: 'queue',
      action: 'queue',
      deviceId: 'a',
      files: [],
      stored: [stored('one.gcode'), stored('two.gcode')],
    });

    await vi.waitFor(() => {
      expect(sent).toEqual(['one.gcode']);
    });
    expect(registry.uploadFor('a')).toBeUndefined();

    answers[0]?.();
    await vi.waitFor(() => {
      expect(sent).toEqual(['one.gcode', 'two.gcode']);
    });
    answers[1]?.();
  });

  // The page stays when the person opens another printer, so the files of a
  // request still on their way go to the printer they were sent to.
  it('sends the rest of a request to its printer after another printer opens', async () => {
    const answers: (() => void)[] = [];
    const sent: string[] = [];
    const sending =
      (device: string) =>
      (call: { body?: string | undefined }): Promise<Response> =>
        new Promise<Response>((resolve) => {
          const body = JSON.parse(call.body ?? '{}') as {
            source: string;
            path: string;
          };
          sent.push(`${device}:${body.source}`);
          answers.push(() => {
            resolve(
              json(200, {
                path: body.path,
                transfer: {
                  transfer_id: `t-${device}-${body.source}`,
                  device_id: device,
                  storage: 'uploads',
                  source: body.source,
                  path: body.path,
                  state: 'done',
                  bytes_sent: 1024,
                  bytes_total: 1024,
                  started_at: '2026-11-18T08:00:00Z',
                },
              }),
            );
          });
        });
    const routes: Routes = {
      'GET /api/v1/devices': json(200, {
        items: [printer('a'), printer('b')],
        total: 2,
      }),
      'GET /api/v1/devices/a/files': listing,
      'GET /api/v1/devices/b/files': listing,
      'GET /api/v1/devices/a/storages': drives,
      'GET /api/v1/devices/b/storages': drives,
      'GET /api/v1/storages': uploads,
      'POST /api/v1/devices/a/files': sending('a'),
      'POST /api/v1/devices/b/files': sending('b'),
    };
    vi.stubGlobal('fetch', stubFetch(routes).fetch);
    const registry = queueing();

    const screen = await render(WithSlots, {
      props: {
        registry,
        screen: PrinterDetail,
        ctx: appContext(routes, { params: { id: 'a', tab: 'status' } }),
      },
    });
    registry.requestUpload({
      namespace: 'queue',
      action: 'queue',
      deviceId: 'a',
      files: [],
      stored: [stored('one.gcode'), stored('two.gcode')],
    });
    await vi.waitFor(() => {
      expect(sent).toEqual(['a:one.gcode']);
    });

    await screen.rerender({
      registry,
      screen: PrinterDetail,
      ctx: appContext(routes, { params: { id: 'b', tab: 'status' } }),
    });
    answers[0]?.();

    await vi.waitFor(() => {
      expect(sent).toEqual(['a:one.gcode', 'a:two.gcode']);
    });
    answers[1]?.();
  });

  // The module's tab shows no drop zone, so a printer with no drive to write
  // to would take the request and say nothing.
  it('says why a printer with no drive takes none of them', async () => {
    const routes: Routes = {
      'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
      'GET /api/v1/devices/a/files': listing,
      'GET /api/v1/devices/a/storages': json(200, {
        items: [],
        reported: true,
      }),
      'GET /api/v1/storages': uploads,
    };
    const fetcher = stubFetch(routes);
    vi.stubGlobal('fetch', fetcher.fetch);
    const said = vi.spyOn(toast, 'error');
    const registry = queueing();

    await render(WithSlots, {
      props: {
        registry,
        screen: PrinterDetail,
        ctx: appContext(routes, { params: { id: 'a', tab: 'status' } }),
      },
    });
    registry.requestUpload({
      namespace: 'queue',
      action: 'queue',
      deviceId: 'a',
      files: [],
      stored: [stored('one.gcode')],
    });

    await vi.waitFor(() => {
      expect(said).toHaveBeenCalledOnce();
    });
    expect(registry.uploadFor('a')).toBeUndefined();
    expect(fetcher.count('POST /api/v1/devices/a/files')).toBe(0);
  });

  // A choice the module never declared has nothing to run on the files, so
  // the page sends none and says so rather than dropping the request.
  it('says that nothing was sent for a choice the module never declared', async () => {
    const routes: Routes = {
      'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
      'GET /api/v1/devices/a/files': listing,
      'GET /api/v1/devices/a/storages': drives,
      'GET /api/v1/storages': uploads,
    };
    const fetcher = stubFetch(routes);
    vi.stubGlobal('fetch', fetcher.fetch);
    const said = vi.spyOn(toast, 'error');
    const registry = queueing();

    await render(WithSlots, {
      props: {
        registry,
        screen: PrinterDetail,
        ctx: appContext(routes, { params: { id: 'a', tab: 'status' } }),
      },
    });
    registry.requestUpload({
      namespace: 'queue',
      action: 'undeclared',
      deviceId: 'a',
      files: [],
      stored: [stored('one.gcode')],
    });

    await vi.waitFor(() => {
      expect(said).toHaveBeenCalledWith(
        'app.printerDetail.undeclaredChoice.title',
        expect.anything(),
      );
    });
    expect(registry.uploadFor('a')).toBeUndefined();
    expect(fetcher.count('POST /api/v1/devices/a/files')).toBe(0);
  });
});

describe('a module tab on the printer page', () => {
  const routes: Routes = {
    'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
  };

  it('opens a module tab from the URL once its manifest is declared', async () => {
    const registry = new SlotRegistry();
    registry.declare('spools', {
      device_tab: [{ id: 'nozzle', title: 'Nozzle' }],
    });
    registry.registerSection('spools', 'device-tab', 'nozzle', PlainSection);

    await render(WithSlots, {
      props: {
        registry,
        screen: PrinterDetail,
        ctx: appContext(routes, { params: { id: 'a', tab: 'spools.nozzle' } }),
      },
    });

    expect(
      document.querySelector('[data-state="active"]')?.textContent.trim(),
    ).toBe('Nozzle');
    await expect
      .element(page.getByText('plain section for a'))
      .toBeInTheDocument();
  });

  it('falls back to status for a module tab no manifest declares', async () => {
    const registry = new SlotRegistry();

    await render(WithSlots, {
      props: {
        registry,
        screen: PrinterDetail,
        ctx: appContext(routes, { params: { id: 'a', tab: 'gone.nozzle' } }),
      },
    });

    // The Status tab's own label, not the slot name: it is the fallback tab.
    expect(
      document.querySelector('[data-state="active"]')?.textContent.trim(),
    ).toBe('Now');
    expect(document.querySelector('[data-testid="plain-section"]')).toBeNull();
  });
});

// While the strip holds a tab whose page is cross-origin isolated, moving the
// focus along it opens no tab, because opening that one loads a new page.
// Where the browser cannot isolate, nothing loads a page, and a strip with no
// such tab keeps opening a tab on focus.
describe('a module tab whose page is cross-origin isolated', () => {
  const routes: Routes = {
    'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
  };
  const isolated = {
    id: 'console',
    title: 'Device page',
    cross_origin_isolated: true,
  };
  const plain = { id: 'log', title: 'Log' };

  afterEach(() => {
    isolation.capable = true;
  });

  // Renders the page on Now with one module tab, moves the focus one tab on,
  // and answers the spy that records where the page went.
  const moveFromNow = async (
    tab: typeof isolated | typeof plain,
  ): Promise<ReturnType<typeof vi.fn>> => {
    const registry = new SlotRegistry();
    registry.declare('console', { device_tab: [tab] });
    const go = vi.fn();
    await render(WithSlots, {
      props: {
        registry,
        screen: PrinterDetail,
        ctx: appContext(routes, { params: { id: 'a', tab: 'status' }, go }),
      },
    });

    await expect
      .element(page.getByRole('tab', { name: tab.title }))
      .toBeVisible();
    page.getByRole('tab', { name: 'Now' }).element().focus();
    await userEvent.keyboard('{ArrowRight}');
    await expect
      .element(page.getByRole('tab', { name: 'Controls' }))
      .toHaveFocus();

    return go;
  };

  it('opens a tab on Enter rather than on focus where the browser can isolate', async () => {
    const go = await moveFromNow(isolated);
    expect(go.mock.calls).toEqual([]);

    await userEvent.keyboard('{Enter}');

    expect(go.mock.calls).toEqual([['/printers/a/controls']]);
  });

  it('opens a tab on focus where the browser cannot isolate', async () => {
    isolation.capable = false;

    const go = await moveFromNow(isolated);

    expect(go.mock.calls).toEqual([['/printers/a/controls']]);
  });

  it('opens a tab on focus when no module tab asks for an isolated page', async () => {
    const go = await moveFromNow(plain);

    expect(go.mock.calls).toEqual([['/printers/a/controls']]);
  });
});

// ADR-20261110-5np: a tab with the devices selector resident: true is on the
// printer the server runs on and on no other.
// One print-progress reading of a printer, as the event stream delivers it.
const progress = (deviceId: string, value: number): StreamEvent => {
  const type = 'com.prusa.connect.device.telemetry.sampled';

  return {
    type,
    id: String(value),
    payload: { path: '/job/progress', value },
    envelope: {
      specversion: '1.0',
      id: String(value),
      source: `urn:prusa:connect:printer/${deviceId}`,
      type,
      time: '2026-11-11T00:00:00Z',
      subject: `device:${deviceId}`,
      hops: 0,
      evclass: 'telemetry',
    },
  };
};

describe('a module tab only for the printer the server runs on', () => {
  const displayRegistry = () => {
    const registry = new SlotRegistry();
    registry.declare('display', {
      device_tab: [
        { id: 'screen', title: 'Display', devices: { resident: true } },
        { id: 'log', title: 'Log' },
      ],
    });
    registry.registerSection('display', 'device-tab', 'screen', PlainSection);

    return registry;
  };

  it('opens on the resident printer', async () => {
    await render(WithSlots, {
      props: {
        registry: displayRegistry(),
        screen: PrinterDetail,
        ctx: appContext(
          {
            'GET /api/v1/devices': json(200, {
              items: [{ ...printer('host'), resident: true }],
              total: 1,
            }),
          },
          { params: { id: 'host', tab: 'display.screen' } },
        ),
      },
    });

    await expect
      .element(page.getByRole('tab', { name: 'Display' }))
      .toHaveAttribute('data-state', 'active');
    await expect
      .element(page.getByText('plain section for host'))
      .toBeInTheDocument();
  });

  it('shows Status until the record arrives, then opens the tab', async () => {
    let answer: ((response: Response) => void) | undefined;
    const held = new Promise<Response>((resolve) => {
      answer = resolve;
    });

    await render(WithSlots, {
      props: {
        registry: displayRegistry(),
        screen: PrinterDetail,
        ctx: appContext(
          { 'GET /api/v1/devices': () => held },
          { params: { id: 'host', tab: 'display.screen' } },
        ),
      },
    });

    await expect
      .element(page.getByRole('tab', { name: 'Log' }))
      .toBeInTheDocument();
    expect(page.getByRole('tab', { name: 'Display' }).query()).toBeNull();
    expect(document.querySelector('[data-testid="plain-section"]')).toBeNull();

    answer?.(
      json(200, { items: [{ ...printer('host'), resident: true }], total: 1 }),
    );
    await expect
      .element(page.getByRole('tab', { name: 'Display' }))
      .toHaveAttribute('data-state', 'active');
  });

  it('keeps the asked tab when the switcher goes back to the resident printer', async () => {
    const go = vi.fn();

    await render(WithSlots, {
      props: {
        registry: displayRegistry(),
        screen: PrinterDetail,
        ctx: appContext(
          {
            'GET /api/v1/devices': json(200, {
              items: [{ ...printer('host'), resident: true }, printer('mk4')],
              total: 2,
            }),
          },
          { params: { id: 'mk4', tab: 'display.screen' }, go },
        ),
      },
    });

    await expect
      .element(page.getByRole('heading', { name: 'Printer mk4' }))
      .toBeInTheDocument();
    await page.getByRole('button', { name: /Printer host/ }).click();

    expect(go).toHaveBeenCalledWith('/printers/host/display.screen');
  });

  // A telemetry value replaces the printer's record about once a second while
  // it prints. The strip is built again only when the record's resident value
  // changes, so the module tabs are not read again on every value.
  it('builds the tab strip again only when resident changes', async () => {
    const registry = displayRegistry();
    const ctx = appContext(
      {
        'GET /api/v1/devices': json(200, {
          items: [{ ...printer('host'), resident: true }],
          total: 1,
        }),
      },
      { params: { id: 'host', tab: 'display.screen' } },
    );
    await render(WithSlots, {
      props: { registry, screen: PrinterDetail, ctx },
    });
    await expect
      .element(page.getByRole('tab', { name: 'Display' }))
      .toHaveAttribute('data-state', 'active');
    const before = ctx.fleet.device('host');
    const reads = vi.spyOn(registry, 'declaredSections');

    for (let value = 1; value <= 5; value += 1) {
      await ctx.fleet.apply(progress('host', value));
    }
    await flush();

    expect(ctx.fleet.device('host')).not.toBe(before);
    expect(
      reads.mock.calls.filter(([slot]) => slot === 'device-tab'),
    ).toHaveLength(0);
  });

  // The printer page passes whether the printer is resident to the section
  // outlet and the action menu as well, so a section or menu item with a
  // devices selector behaves like the tab.
  const sectionAndActions = () => {
    const registry = new SlotRegistry();
    registry.declare('display', {
      device_section: [
        { id: 'panel', title: 'Panel', devices: { resident: true } },
      ],
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
    registry.registerSection(
      'display',
      'device-section',
      'panel',
      PlainSection,
    );
    registry.registerAction('display', 'device-action', 'wake', () =>
      Promise.resolve(),
    );
    registry.registerAction('display', 'device-action', 'log', () =>
      Promise.resolve(),
    );

    return registry;
  };

  const drawOn = async (
    device: ReturnType<typeof printer> & { resident?: boolean },
  ): Promise<void> => {
    await render(WithSlots, {
      props: {
        registry: sectionAndActions(),
        screen: PrinterDetail,
        ctx: appContext(
          { 'GET /api/v1/devices': json(200, { items: [device], total: 1 }) },
          { params: { id: device.id } },
        ),
      },
    });
    await expect
      .element(page.getByRole('heading', { name: device.name }))
      .toBeInTheDocument();
  };

  it('draws a selected section and menu item on the resident printer', async () => {
    await drawOn({ ...printer('host'), resident: true });

    await expect
      .element(page.getByText('plain section for host'))
      .toBeInTheDocument();
    await page
      .getByRole('button', { name: /Actions for Printer host/ })
      .click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Wake' }))
      .toBeVisible();
  });

  it('draws no selected section or menu item on another printer', async () => {
    await drawOn(printer('mk4'));

    await page.getByRole('button', { name: /Actions for Printer mk4/ }).click();
    await expect
      .element(page.getByRole('menuitem', { name: 'Log' }))
      .toBeVisible();
    expect(page.getByRole('menuitem', { name: 'Wake' }).query()).toBeNull();
    expect(document.querySelector('[data-testid="plain-section"]')).toBeNull();
  });

  it('is no tab of another printer, and its link opens Status', async () => {
    await render(WithSlots, {
      props: {
        registry: displayRegistry(),
        screen: PrinterDetail,
        ctx: appContext(
          {
            'GET /api/v1/devices': json(200, {
              items: [printer('mk4')],
              total: 1,
            }),
          },
          { params: { id: 'mk4', tab: 'display.screen' } },
        ),
      },
    });

    // The record has arrived once the header names the printer, so the strip
    // below is the one drawn for it and not the one before it loaded.
    await expect
      .element(page.getByRole('heading', { name: 'Printer mk4' }))
      .toBeInTheDocument();
    await expect
      .element(page.getByRole('tab', { name: 'Log' }))
      .toBeInTheDocument();
    expect(page.getByRole('tab', { name: 'Display' }).query()).toBeNull();
    expect(
      document.querySelector('[data-state="active"]')?.textContent.trim(),
    ).toBe('Now');
    expect(document.querySelector('[data-testid="plain-section"]')).toBeNull();
  });
});

// A transfers route whose answers the test gives, one per request, in order.
const heldTransfers = (): {
  handler: () => Promise<Response>;
  answer: (items: unknown[]) => Promise<void>;
} => {
  const waiting: ((items: unknown[]) => void)[] = [];

  return {
    handler: () =>
      new Promise<Response>((resolve) => {
        waiting.push((items) => {
          resolve(json(200, { items }));
        });
      }),
    answer: async (items) => {
      await vi.waitFor(() => {
        expect(waiting.length).toBeGreaterThan(0);
      });
      waiting.shift()?.(items);
    },
  };
};

const sending = (id: string, state = 'sending') => ({
  transfer_id: id,
  storage: 'local',
  source: `${id}.gcode`,
  path: `usb/${id}.gcode`,
  bytes_total: 10,
  bytes_sent: state === 'done' ? 10 : 5,
  state,
  started_at: '2026-09-25T08:00:00Z',
});

// Every event stream the page opens, so a test can write one frame to all.
const streams = (): {
  handler: () => Response;
  push: (chunk: string) => void;
} => {
  const open: Stream[] = [];

  return {
    handler: () => {
      const one = stream();
      open.push(one);

      return one.response;
    },
    push: (chunk) => {
      for (const one of open) one.push(chunk);
    },
  };
};

const flush = (): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, 50));

describe('the landing toasts on the printer page', () => {
  // The read of the printer the page left must not stand for the read of the
  // printer it shows now.
  it('still announces a landing after a late read of the printer it left', async () => {
    const success = vi.spyOn(toast, 'success').mockReturnValue('');
    const onA = heldTransfers();
    const onB = heldTransfers();
    const events = streams();
    const routes: Routes = {
      'GET /api/v1/devices': json(200, {
        items: [printer('a'), printer('b')],
        total: 2,
      }),
      'GET /api/v1/devices/a/transfers': onA.handler,
      'GET /api/v1/devices/b/transfers': onB.handler,
      'GET /api/v1/events': events.handler,
    };
    vi.stubGlobal('fetch', stubFetch(routes).fetch);

    const { rerender } = await render(PrinterDetail, {
      props: {
        ctx: appContext(routes, { params: { id: 'a', tab: 'status' } }),
      },
    });
    await rerender({
      ctx: appContext(routes, { params: { id: 'b', tab: 'status' } }),
    });
    await onB.answer([sending('t-1')]);
    await flush();
    await onA.answer([]);
    await flush();

    events.push(
      frame('9', 'com.prusa.connect.device.transfer.settled', {
        subject: 'device:b',
        source: 'urn:prusa:site:site-1/device/b',
        data: {
          transfer_id: 't-1',
          state: 'done',
          destination: 'usb/t-1.gcode',
        },
      }),
    );

    await vi.waitFor(() => {
      expect(success).toHaveBeenCalledWith('t-1.gcode is on the printer.');
    });
    success.mockRestore();
  });

  // Coming back to a printer, its shared store still holds the rows of the
  // first visit. A transfer that ended while the person was away is history.
  it('says nothing about a transfer that ended while the page showed another printer', async () => {
    const success = vi.spyOn(toast, 'success').mockReturnValue('');
    const onA = heldTransfers();
    const onB = heldTransfers();
    const routes: Routes = {
      'GET /api/v1/devices': json(200, {
        items: [printer('a'), printer('b')],
        total: 2,
      }),
      'GET /api/v1/devices/a/transfers': onA.handler,
      'GET /api/v1/devices/b/transfers': onB.handler,
    };
    vi.stubGlobal('fetch', stubFetch(routes).fetch);
    const ctx = (id: string) =>
      appContext(routes, { params: { id, tab: 'status' } });
    // One context for the whole visit, so the stores are the shared ones.
    const shared = ctx('a');

    const { rerender } = await render(PrinterDetail, {
      props: { ctx: shared },
    });
    await onA.answer([sending('t-1')]);
    await flush();

    await rerender({ ctx: { ...shared, params: { id: 'b', tab: 'status' } } });
    await rerender({ ctx: { ...shared, params: { id: 'a', tab: 'status' } } });
    await flush();
    await onA.answer([sending('t-1', 'done')]);
    await onB.answer([]);
    await flush();

    expect(success).not.toHaveBeenCalled();
    success.mockRestore();
  });
  // A replace fails before a byte moves when the printer will not delete its
  // file, so no transfer row says it: the page says it from the answer.
  it('says a replace the printer refused, with the reason', async () => {
    const error = vi.spyOn(toast, 'error').mockReturnValue('');
    const routes: Routes = {
      'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
      'GET /api/v1/devices/a/files': listing,
      'POST /api/v1/devices/a/files': (call) =>
        call.body?.includes('"replace":true') === true
          ? problem(409, 'core.printer/STORAGE_REFUSED')
          : problem(409, 'core.printer/FILE_NAME_TAKEN'),
    };
    vi.stubGlobal('fetch', stubFetch(routes).fetch);
    const ctx = appContext(routes, { params: { id: 'a', tab: 'status' } });
    await render(PrinterDetail, { props: { ctx } });

    void ctx.transfers('a').send('local', 'one.gcode', 'usb/one.gcode');
    await page.getByRole('button', { name: 'Replace' }).click();

    await vi.waitFor(() => {
      expect(String(error.mock.calls[0]?.[0])).toMatch(
        /^one\.gcode did not reach the printer: /u,
      );
    });
    expect(error).toHaveBeenCalledOnce();
    error.mockRestore();
  });

  // The printer refuses to delete a file it lists read-only, and the refusal
  // says so, so the question offers no Replace.
  it('offers no replace of a file the printer lists read-only', async () => {
    const routes: Routes = {
      'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
      'POST /api/v1/devices/a/files': problem(
        409,
        'core.printer/FILE_NAME_TAKEN',
        { read_only: true },
      ),
    };
    vi.stubGlobal('fetch', stubFetch(routes).fetch);
    const ctx = appContext(routes, { params: { id: 'a', tab: 'status' } });
    await render(PrinterDetail, { props: { ctx } });

    void ctx.transfers('a').send('local', 'one.gcode', 'usb/one.gcode');

    await expect
      .element(
        page.getByText(
          'A file named one.gcode is already on this printer and cannot be replaced.',
        ),
      )
      .toBeVisible();
    expect(
      page.getByRole('button', { name: 'Replace' }).elements(),
    ).toHaveLength(0);
  });

  // ADR-20261106-xm4 rule 3: the Cameras tab is on the page when the printer
  // has a camera or the person may add one, and never only to say it is empty.
  it.each([
    [
      'a printer with a camera',
      [
        {
          name: 'bed-cam',
          device_id: 'a',
          formats: ['jpeg'],
          serving: true,
          snapshot: true,
          stream: false,
          watch: false,
        },
      ],
      false,
      true,
    ],
    ['a person who may add a camera', [], true, true],
    ['nobody who may add one, and no camera', [], false, false],
  ])('draws the Cameras tab for %s', async (_, items, allowed, drawn) => {
    const routes: Routes = {
      'GET /api/v1/devices': json(200, { items: [printer('a')], total: 1 }),
      'GET /api/v1/cameras': json(200, { items }),
      'GET /api/v1/camera-settings': json(200, { items: [] }),
      'POST /api/v1/access/check': json(200, { allowed }),
    };
    vi.stubGlobal('fetch', stubFetch(routes).fetch);

    await render(PrinterDetail, {
      props: {
        ctx: appContext(routes, { params: { id: 'a', tab: 'status' } }),
      },
    });

    await expect.element(page.getByRole('tab', { name: 'Now' })).toBeVisible();
    const tab = page.getByRole('tab', { name: 'Cameras' });
    if (drawn) {
      await expect.element(tab).toBeVisible();
    } else {
      // Long enough for both reads to answer.
      await new Promise((resolve) => setTimeout(resolve, 300));
      expect(tab.query()).toBe(null);
    }
  });
});
