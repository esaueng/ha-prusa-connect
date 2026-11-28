import {
  formatDuration,
  locale,
  registerCatalogue,
} from '@prusa/connect-ui/i18n';
import { BoxGeometry, Matrix4 } from 'three';
import { STLLoader } from 'three/addons/loaders/STLLoader.js';
import { page, userEvent } from 'vitest/browser';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import type { ModuleHost } from '@prusa/connect-ui/slots';

import czech from '../../locales/cs.json';
import { t } from '../../test/fakes';
import type { QueueBlock } from '../../plate/printers';
import { makeSTL } from './model';
import type { Job } from '../../api';
import { type PrepareApi, prepareOn } from './prepare-api';
import {
  assembledFile,
  bed,
  charlie,
  henry,
  loadFixture,
  memory,
  modelFile,
  placedJob,
  profileSettings,
  slicedJob,
  testPrepare,
  xl,
} from './prepare-fixtures';
import PreparePanel from './prepare-panel.svelte';

/** An API that answers like the fixtures and records each call. */
const recording = (more: Partial<PrepareApi> = {}) => ({
  destinations: vi.fn(more.destinations ?? testPrepare.destinations),
  read: vi.fn(more.read ?? testPrepare.read),
  save: vi.fn(more.save ?? testPrepare.save),
  submit: vi.fn(more.submit ?? testPrepare.submit),
  job: vi.fn(more.job ?? testPrepare.job),
  download: vi.fn(more.download ?? testPrepare.download),
  print: vi.fn(more.print ?? testPrepare.print),
  checkQueue: vi.fn(more.checkQueue ?? testPrepare.checkQueue),
  enqueue: vi.fn(more.enqueue ?? testPrepare.enqueue),
});

const xlBed = { min_x: 0, min_y: 0, max_x: 360, max_y: 360, height: 360 };

const props = (api: PrepareApi, more: Record<string, unknown> = {}) => ({
  api,
  beds: { 'core-one-petg': bed, 'xl-pla': xlBed },
  profileSettings: {
    ...profileSettings,
    'xl-pla': { filament_type: 'PLA', layer_height_mm: 0.2 },
  },
  hasSlicer: true,
  devices: [henry, charlie, xl],
  remember: memory(),
  source: 'local:parts/bracket.stl',
  t,
  ...more,
});

const status = () => page.getByTestId('plate-status');

describe('the build-plate workspace', () => {
  it('slices the plate and prints it on the idle printer', async () => {
    const api = recording();
    await page.viewport(1440, 900);
    await render(PreparePanel, { props: props(api) });

    await expect.element(status()).toMatchTextContent('1 part');
    expect(api.read).toHaveBeenCalledWith(
      'local:parts/bracket.stl',
      expect.any(AbortSignal),
    );
    await expect
      .element(status())
      .toMatchTextContent('Henry · bed 250 × 220 mm');
    const print = page.getByRole('button', { name: 'Print', exact: true });
    await expect.element(print).toBeEnabled();
    // No slice has run, so the button shows no time.
    expect(print.element().textContent).not.toContain(formatDuration(12_300));

    await print.click();
    await expect.element(page.getByText('Printing on Henry')).toBeVisible();
    expect(api.submit).toHaveBeenCalledWith(
      expect.objectContaining({
        profile: 'core-one-petg',
        settings: undefined,
      }),
      expect.any(AbortSignal),
    );
    expect(api.print).toHaveBeenCalledWith(
      {
        deviceId: 'henry',
        storage: 'local',
        source: slicedJob.result?.path,
        name: '3f9a01c2-bracket-placed-core-one-petg.gcode',
      },
      expect.any(AbortSignal),
      expect.any(Function),
    );
    // The time comes from the finished slice of this plate.
    await expect
      .element(page.getByTestId('print-button'))
      .toMatchTextContent(formatDuration(12_300));
  });

  it.each([
    ['queueChecking', 'Checking the Print queue.'],
    ['queueInstall', 'Install the Print queue module.'],
    ['queueOffline', 'Start the Print queue module.'],
    ['queueApproval', 'Approve the queue permission for Slicing.'],
    ['queueDenied', 'You may not add prints to this queue.'],
    ['queuePrinterMissing', 'Print queue cannot find this printer.'],
    ['queuePrinterUnknown', 'Print queue could not read this printer.'],
    ['queueUnavailable', 'Print queue is unavailable.'],
  ] satisfies readonly (readonly [QueueBlock, string])[])(
    'keeps Add to queue disabled and explains %s',
    async (block, reason) => {
      const remember = memory();
      remember.write('plate.printer', 'charlie');
      const api = recording({
        checkQueue: () =>
          block === 'queueChecking'
            ? new Promise(() => undefined)
            : Promise.resolve(block),
      });
      await page.viewport(1440, 900);
      await render(PreparePanel, { props: props(api, { remember }) });

      await expect.element(status()).toMatchTextContent('1 part');
      const queue = page.getByRole('button', { name: 'Add to queue' });
      await expect.element(queue).toBeDisabled();
      await expect.element(status()).toMatchTextContent(reason);
      await expect.element(queue).toHaveAccessibleDescription(reason);
      expect(api.checkQueue).toHaveBeenCalledWith(
        'charlie',
        expect.any(AbortSignal),
      );
      expect(api.enqueue).not.toHaveBeenCalled();
    },
  );

  it('slices for a busy printer and refreshes its waiting count when the job is added', async () => {
    const remember = memory();
    remember.write('plate.printer', 'charlie');
    const api = recording({ submit: () => Promise.resolve(slicedJob) });
    await page.viewport(1440, 900);
    await render(PreparePanel, { props: props(api, { remember }) });

    const queue = page.getByRole('button', { name: /Add to queue/u });
    await expect.element(queue).toBeEnabled();
    expect(api.enqueue).not.toHaveBeenCalled();
    await queue.click();
    await expect
      .element(
        page.getByText('Added to the queue for Charlie', { exact: true }),
      )
      .toBeVisible();
    await expect.element(status()).toMatchTextContent('2 prints waiting');
    expect(api.enqueue).toHaveBeenCalledExactlyOnceWith(
      slicedJob.id,
      'charlie',
      expect.any(AbortSignal),
    );
    expect(api.print).not.toHaveBeenCalled();

    api.enqueue.mockResolvedValueOnce({ waiting: 3 });
    await queue.click();
    await expect
      .element(
        page.getByText('Added to the queue for Charlie', { exact: true }),
      )
      .toBeVisible();
    await expect.element(status()).toMatchTextContent('3 prints waiting');
    expect(api.submit).toHaveBeenCalledTimes(1);

    await page.getByTestId('printer-picker').click();
    await page.getByRole('menuitemradio', { name: /Henry/u }).click();
    await expect.element(status()).not.toMatchTextContent('3 prints waiting');
  });

  it('rechecks a queue that was unavailable without changing the selected printer', async () => {
    const remember = memory();
    remember.write('plate.printer', 'charlie');
    const api = recording();
    api.checkQueue.mockResolvedValueOnce('queueUnavailable');
    await render(PreparePanel, { props: props(api, { remember }) });

    const queue = page.getByRole('button', { name: 'Add to queue' });
    await expect.element(queue).toBeDisabled();
    await page.getByRole('button', { name: 'Retry queue check' }).click();
    await expect.element(queue).toBeEnabled();
    expect(api.checkQueue).toHaveBeenCalledTimes(2);
    expect(api.checkQueue).toHaveBeenLastCalledWith(
      'charlie',
      expect.any(AbortSignal),
    );
    expect(api.enqueue).not.toHaveBeenCalled();
  });

  it.each([
    'server-slicing/PRINT_QUEUE_UNAVAILABLE',
    'server-slicing/PRINT_QUEUE_PRINTER_UNKNOWN',
  ])('allows another enqueue after a transient %s refusal', async (code) => {
    const remember = memory();
    remember.write('plate.printer', 'charlie');
    const api = recording({ submit: () => Promise.resolve(slicedJob) });
    api.enqueue.mockRejectedValueOnce(
      Object.assign(new Error('Try again'), { code, retryable: true }),
    );
    await render(PreparePanel, { props: props(api, { remember }) });

    const queue = page.getByRole('button', { name: /Add to queue/u });
    await expect.element(queue).toBeEnabled();
    await queue.click();
    await expect.element(page.getByText(code, { exact: true })).toBeVisible();
    await expect.element(queue).toBeEnabled();
    await queue.click();

    await expect.element(status()).toMatchTextContent('2 prints waiting');
    expect(api.enqueue).toHaveBeenCalledTimes(2);
    expect(api.submit).toHaveBeenCalledOnce();
  });

  it('shows a queue refusal in the current language', async () => {
    registerCatalogue('com.github.aux.prusa3d.server-slicing', 'cs', czech);
    const remember = memory();
    remember.write('plate.printer', 'charlie');
    const api = recording({
      submit: () => Promise.resolve(slicedJob),
      enqueue: () =>
        Promise.reject(
          Object.assign(new Error('the queue of this printer is full'), {
            code: 'server-slicing/PRINT_QUEUE_FULL',
          }),
        ),
    });
    locale.set('cs');
    try {
      await render(PreparePanel, { props: props(api, { remember }) });
      const queue = page.getByRole('button', {
        name: 'Přidat do fronty',
        exact: true,
      });
      await expect.element(queue).toBeEnabled();
      await queue.click();
      await expect
        .element(page.getByText('Fronta této tiskárny je plná'))
        .toBeVisible();
      expect(api.print).not.toHaveBeenCalled();
    } finally {
      locale.set('en');
    }
  });

  it('resizes the bed to the printer chosen and keeps the choice', async () => {
    const remember = memory();
    await page.viewport(1440, 900);
    await render(PreparePanel, {
      props: props(recording(), { remember, source: undefined }),
    });

    await page.getByTestId('printer-picker').click();
    await page.getByRole('menuitemradio', { name: /Xavier/u }).click();
    await expect
      .element(status())
      .toMatchTextContent('Xavier · bed 360 × 360 mm');
    expect(remember.read('plate.printer')).toBe('xl');
    await expect
      .element(page.getByText('Add a model to the plate.'))
      .toBeVisible();
  });

  it('rotates with R, removes with Delete and takes it back with Ctrl+Z', async () => {
    // A part 10 × 30 mm near the right edge fits until it is turned.
    const geometry = new BoxGeometry(10, 30, 10);
    const file = new File(
      [
        makeSTL([
          { geometry, matrix: new Matrix4().makeTranslation(240, 100, 5) },
        ]),
      ],
      'tall.stl',
    );
    geometry.dispose();
    await page.viewport(1440, 900);
    await render(PreparePanel, {
      props: props(recording({ read: () => Promise.resolve(file) })),
    });
    await expect.element(status()).toMatchTextContent('1 part');
    const print = page.getByRole('button', { name: 'Print', exact: true });
    await expect.element(print).toBeEnabled();

    await userEvent.keyboard('r');
    await expect
      .element(status())
      .toMatchTextContent('A part is outside the print volume.');
    await expect.element(print).toBeDisabled();

    await userEvent.keyboard('{Control>}z{/Control}');
    await expect.element(print).toBeEnabled();

    await userEvent.keyboard('{Delete}');
    await expect.element(status()).toMatchTextContent('0 parts');
    await userEvent.keyboard('{Control>}z{/Control}');
    await expect.element(status()).toMatchTextContent('1 part');
  });

  it('scales the selected part from the toolbar and blocks a part grown past the volume', async () => {
    const api = recording();
    await page.viewport(1440, 900);
    await render(PreparePanel, { props: props(api) });
    await expect.element(status()).toMatchTextContent('1 part');
    const print = page.getByRole('button', { name: 'Print', exact: true });
    const scale = page.getByLabelText('Scale (%)');
    await expect.element(scale).toHaveValue(100);

    // The box is 30 mm tall and the bed 270 mm, so ten times is too tall.
    await scale.fill('1000');
    await userEvent.keyboard('{Enter}');
    await expect
      .element(status())
      .toMatchTextContent('A part is outside the print volume.');
    await expect.element(print).toBeDisabled();

    await scale.fill('5000');
    await userEvent.keyboard('{Enter}');
    await expect.element(scale).toHaveValue(1000);

    await scale.fill('150');
    await userEvent.keyboard('{Enter}');
    await expect.element(print).toBeEnabled();

    // The scale goes with the selection.
    await page
      .getByLabelText('3D build plate view')
      .click({ position: { x: 5, y: 5 } });
    await expect.element(scale).not.toBeInTheDocument();
  });

  it('sends the settings chosen, and keeps them for the printer', async () => {
    const api = recording();
    const remember = memory();
    await page.viewport(1440, 900);
    await render(PreparePanel, { props: props(api, { remember }) });
    await expect.element(status()).toMatchTextContent('1 part');

    await page.getByTestId('print-settings').click();
    await page.getByRole('radio', { name: 'Everywhere' }).click();
    await page.getByRole('button', { name: 'One wall more' }).click();
    await userEvent.keyboard('{Escape}');

    await page
      .getByRole('button', { name: 'More ways to finish the plate' })
      .click();
    await page.getByRole('menuitem', { name: 'Slice only' }).click();
    await expect.poll(() => api.submit.mock.calls.length).toBe(1);
    expect(api.submit.mock.calls[0]?.[0].settings).toEqual({
      supports: 'everywhere',
      walls: 3,
    });
    expect(api.print).not.toHaveBeenCalled();
    expect(remember.read('plate.choices.henry')).toContain('"walls":3');
  });

  it('fits a narrow screen without scrolling sideways', async () => {
    await page.viewport(390, 844);
    await render(PreparePanel, {
      props: props(recording(), { source: undefined }),
    });
    await expect
      .element(page.getByRole('button', { name: 'Add' }))
      .toBeVisible();
    await expect
      .element(page.getByRole('button', { name: 'Print', exact: true }))
      .toBeDisabled();
    expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(390);
  });
});

describe('a 3MF on the build plate', () => {
  const pick = async (container: HTMLElement, file: File): Promise<void> => {
    const picker =
      container.querySelector<HTMLInputElement>('input[type="file"]');
    if (!picker) throw new Error('The workspace drew no file picker');
    await page.elementLocator(picker).upload(file);
  };

  it.each(['pick', 'drop'])(
    'puts each object of a 3MF on the plate by %s, where the file put it',
    async (input) => {
      const api = recording();
      await page.viewport(1440, 900);
      const { container } = await render(PreparePanel, {
        props: props(api, { source: undefined }),
      });

      const file = await assembledFile();
      if (input === 'pick') {
        await pick(container, file);
      } else {
        const dataTransfer = new DataTransfer();
        dataTransfer.items.add(file);
        page
          .getByLabelText('3D build plate view')
          .element()
          .dispatchEvent(
            new DragEvent('drop', { bubbles: true, dataTransfer }),
          );
      }

      // The two instances are two parts, not one merged mesh.
      await expect.element(status()).toMatchTextContent('2 parts');
      await expect
        .element(page.getByText(/Apply any cuts made with negative volumes/u))
        .toBeVisible();

      await page
        .getByRole('button', { name: 'More ways to finish the plate' })
        .click();
      await page.getByRole('menuitem', { name: 'Save positioned STL' }).click();
      await expect.poll(() => api.save.mock.calls.length).toBe(1);
      const [, name, body] = api.save.mock.calls[0] ?? [];
      expect(name).toMatch(/^assembled-placed-.+\.stl$/u);
      if (!body) throw new Error('No plate was saved');
      const written = new STLLoader().parse(await body.arrayBuffer());
      expect(written.getAttribute('position').count).toBe(24);
      written.computeBoundingBox();
      const bounds = written.boundingBox;
      if (!bounds) throw new Error('The plate has no bounds');
      expect(bounds.min.x).toBeCloseTo(66.2);
      expect(bounds.min.y).toBeCloseTo(25.4);
      expect(bounds.min.z).toBe(0);
      expect(bounds.max.x).toBeCloseTo(127);
      expect(bounds.max.y).toBeCloseTo(86.2);
      expect(bounds.max.z).toBeCloseTo(22.7);
      written.dispose();
    },
  );

  it('says when a 3MF expands beyond the import limits', async () => {
    const { container } = await render(PreparePanel, {
      props: props(recording(), { source: undefined }),
    });
    await pick(container, await loadFixture('large-archive.3mf'));
    await expect
      .element(page.getByText(/The model exceeds the import limits/u))
      .toBeVisible();
    await expect.element(status()).toMatchTextContent('0 parts');
  });

  it('asks for geometry only when a project has unknown part roles', async () => {
    const { container } = await render(PreparePanel, {
      props: props(recording(), { source: undefined }),
    });
    await pick(container, await loadFixture('bambu-unknown-role.3mf'));
    await expect
      .element(page.getByText(/Could not determine the 3MF part roles/u))
      .toBeVisible();
    await expect
      .element(page.getByRole('button', { name: 'Print', exact: true }))
      .toBeDisabled();
  });
});

describe('what the review found', () => {
  it('sends nothing when the plate and the printer change while it is sliced', async () => {
    let finish: (job: Job) => void = () => undefined;
    const api = recording({
      submit: () =>
        new Promise<Job>((resolve) => {
          finish = resolve;
        }),
    });
    await page.viewport(1440, 900);
    await render(PreparePanel, { props: props(api) });
    await expect.element(status()).toMatchTextContent('1 part');

    await page.getByRole('button', { name: 'Print', exact: true }).click();
    await expect.poll(() => api.submit.mock.calls.length).toBe(1);
    await page.getByTestId('printer-picker').click();
    await page.getByRole('menuitemradio', { name: /Xavier/u }).click();
    await userEvent.keyboard('{Delete}');
    await expect.element(status()).toMatchTextContent('0 parts');

    finish(placedJob);
    await expect
      .element(page.getByText(/The plate changed while it was sliced/u))
      .toBeVisible();
    expect(api.print).not.toHaveBeenCalled();
  });

  it('shows and slices with the one profile the bed was matched to', async () => {
    const api = recording();
    const remember = memory();
    remember.write('plate.printer', 'xl');
    await page.viewport(1440, 900);
    await render(PreparePanel, { props: props(api, { remember }) });
    await expect.element(status()).toMatchTextContent('1 part');

    await expect
      .element(page.getByTestId('print-settings'))
      .toMatchTextContent('PLA');
    await page.getByTestId('print-settings').click();
    await expect
      .element(page.getByLabelText('Material'))
      .toMatchTextContent('PLA · xl-pla');
    await userEvent.keyboard('{Escape}');

    await page
      .getByRole('button', { name: 'More ways to finish the plate' })
      .click();
    await page.getByRole('menuitem', { name: 'Slice only' }).click();
    await expect.poll(() => api.submit.mock.calls.length).toBe(1);
    expect(api.submit.mock.calls[0]?.[0].profile).toBe('xl-pla');
  });

  it('adds every file dropped together and names each one it cannot read', async () => {
    await page.viewport(1440, 900);
    await render(PreparePanel, {
      props: props(recording(), { source: undefined }),
    });
    const drop = (files: File[]): void => {
      const dataTransfer = new DataTransfer();
      for (const file of files) dataTransfer.items.add(file);
      page
        .getByLabelText('3D build plate view')
        .element()
        .dispatchEvent(new DragEvent('drop', { bubbles: true, dataTransfer }));
    };

    drop([
      modelFile('first.stl'),
      modelFile('second.stl'),
      modelFile('third.stl'),
    ]);
    await expect.element(status()).toMatchTextContent('3 parts');

    drop([
      new File(['not a mesh at all'], 'broken.stl'),
      modelFile('fourth.stl'),
    ]);
    await expect.element(status()).toMatchTextContent('4 parts');
    await expect
      .element(page.getByText(/^broken\.stl: Could not open the model/u))
      .toBeVisible();
  });
});

describe('what the re-check found', () => {
  /** An API whose slice ends only when the test says so. */
  const held = () => {
    let finish: (job: Job) => void = () => undefined;
    const api = recording({
      submit: () =>
        new Promise<Job>((resolve) => {
          finish = resolve;
        }),
    });
    return {
      api,
      finish: (job: Job) => {
        finish(job);
      },
    };
  };

  it('sends nothing when the printer’s bed shrinks while the plate is sliced', async () => {
    const { api, finish } = held();
    await page.viewport(1440, 900);
    const screen = await render(PreparePanel, { props: props(api) });
    await expect.element(status()).toMatchTextContent('1 part');

    await page.getByRole('button', { name: 'Print', exact: true }).click();
    await expect.poll(() => api.submit.mock.calls.length).toBe(1);
    const small = {
      ...henry,
      build_volume: { x_mm: 100, y_mm: 100, z_mm: 20 },
    };
    await screen.rerender({ devices: [small, charlie, xl] });
    await expect
      .element(status())
      .toMatchTextContent('Henry · bed 100 × 100 mm');

    finish(placedJob);
    await expect
      .element(page.getByText(/The plate changed while it was sliced/u))
      .toBeVisible();
    expect(api.print).not.toHaveBeenCalled();
  });

  it('sends nothing once the workspace closes while the plate is sliced', async () => {
    let finish: (job: Job) => void = () => undefined;
    const api = recording({
      job: () =>
        new Promise<Job>((resolve) => {
          finish = resolve;
        }),
    });
    await page.viewport(1440, 900);
    const screen = await render(PreparePanel, { props: props(api) });
    await expect.element(status()).toMatchTextContent('1 part');

    await page.getByRole('button', { name: 'Print', exact: true }).click();
    // The first reading of the job comes after one poll interval.
    await expect
      .poll(() => api.job.mock.calls.length, { timeout: 5000 })
      .toBe(1);
    await screen.unmount();
    finish(slicedJob);
    // Give the print every chance to run.
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(api.print).not.toHaveBeenCalled();
  });

  it('adds a file dropped while a model from Storage is still read', async () => {
    let read: (file: File) => void = () => undefined;
    const api = recording({
      read: () =>
        new Promise<File>((resolve) => {
          read = resolve;
        }),
    });
    await page.viewport(1440, 900);
    await render(PreparePanel, { props: props(api) });
    await expect.poll(() => api.read.mock.calls.length).toBe(1);

    const dataTransfer = new DataTransfer();
    dataTransfer.items.add(modelFile('dropped.stl'));
    page
      .getByLabelText('3D build plate view')
      .element()
      .dispatchEvent(new DragEvent('drop', { bubbles: true, dataTransfer }));
    read(modelFile('from-storage.stl'));

    await expect.element(status()).toMatchTextContent('2 parts');
    expect(page.getByRole('alert').elements()).toHaveLength(0);
  });
});

describe('what the second re-check found', () => {
  it('starts nothing when the bed shrinks while the printer’s storages are read', async () => {
    let storages: (page: unknown) => void = () => undefined;
    const printDeviceFile = vi.fn(() =>
      Promise.resolve({
        command: { command_id: 'c1', ok: true },
        path: 'usb/a.gcode',
      }),
    );
    const client = {
      listDeviceStorages: () =>
        new Promise((resolve) => {
          storages = resolve;
        }),
      printDeviceFile,
    };
    const api: PrepareApi = {
      ...prepareOn(
        // The print path calls these two operations of the client and no other.
        client as unknown as ModuleHost['client'],
        {
          submitJob: () => Promise.resolve(placedJob),
          readJob: () => Promise.resolve(slicedJob),
        },
      ),
      destinations: testPrepare.destinations,
      read: testPrepare.read,
      save: testPrepare.save,
    };
    await page.viewport(1440, 900);
    const screen = await render(PreparePanel, { props: props(api) });
    await expect.element(status()).toMatchTextContent('1 part');

    await page.getByRole('button', { name: 'Print', exact: true }).click();
    await expect
      .element(page.getByTestId('print-button'))
      .toMatchTextContent('Sending');
    const small = {
      ...henry,
      build_volume: { x_mm: 100, y_mm: 100, z_mm: 20 },
    };
    await screen.rerender({ devices: [small, charlie, xl] });
    await expect.element(status()).toMatchTextContent('bed 100 × 100 mm');

    storages({
      reported: true,
      items: [
        {
          name: 'usb',
          mountpoint: '/usb',
          read_only: false,
          type: 'USB',
          free_space: 1,
        },
      ],
    });
    await expect
      .element(page.getByText(/The plate changed while it was sliced/u))
      .toBeVisible();
    expect(printDeviceFile).not.toHaveBeenCalled();
  });

  it('sends nothing when the printer starts printing while the plate is sliced', async () => {
    // Henry is chosen, so only its state changes when it starts printing.
    const remember = memory();
    remember.write('plate.printer', 'henry');
    let finish: (job: Job) => void = () => undefined;
    const api = recording({
      submit: () =>
        new Promise<Job>((resolve) => {
          finish = resolve;
        }),
    });
    await page.viewport(1440, 900);
    const screen = await render(PreparePanel, {
      props: props(api, { remember }),
    });
    await expect.element(status()).toMatchTextContent('1 part');

    await page.getByRole('button', { name: 'Print', exact: true }).click();
    await expect.poll(() => api.submit.mock.calls.length).toBe(1);
    const busy = { ...henry, state: { ...henry.state, '/state': 'PRINTING' } };
    await screen.rerender({ devices: [busy, charlie, xl] });

    await expect
      .element(page.getByTestId('printer-picker'))
      .toMatchTextContent('Henry');

    finish(placedJob);
    await expect
      .element(page.getByText(/The plate changed while it was sliced/u))
      .toBeVisible();
    expect(api.print).not.toHaveBeenCalled();
  });

  it('keeps the printer Print was pressed for when the list reorders', async () => {
    const api = recording({
      submit: () => new Promise<Job>(() => undefined),
    });
    await page.viewport(1440, 900);
    const screen = await render(PreparePanel, { props: props(api) });
    await expect.element(status()).toMatchTextContent('1 part');
    await page.getByRole('button', { name: 'Print', exact: true }).click();
    await expect.poll(() => api.submit.mock.calls.length).toBe(1);

    // Henry starting a print would put Xavier first in the list.
    const busy = { ...henry, state: { ...henry.state, '/state': 'PRINTING' } };
    await screen.rerender({ devices: [busy, charlie, xl] });
    await expect
      .element(page.getByTestId('printer-picker'))
      .toMatchTextContent('Henry');
  });

  it('reads no queued file once the workspace has closed', async () => {
    let read: (file: File) => void = () => undefined;
    const api = recording({
      read: () =>
        new Promise<File>((resolve) => {
          read = resolve;
        }),
    });
    await page.viewport(1440, 900);
    const screen = await render(PreparePanel, { props: props(api) });
    await expect.poll(() => api.read.mock.calls.length).toBe(1);

    const queued = modelFile('queued.stl');
    const opened = vi.spyOn(queued, 'arrayBuffer');
    const dataTransfer = new DataTransfer();
    dataTransfer.items.add(queued);
    page
      .getByLabelText('3D build plate view')
      .element()
      .dispatchEvent(new DragEvent('drop', { bubbles: true, dataTransfer }));
    await screen.unmount();
    const stored = modelFile('from-storage.stl');
    const storedOpened = vi.spyOn(stored, 'arrayBuffer');
    read(stored);

    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(storedOpened).not.toHaveBeenCalled();
    expect(opened).not.toHaveBeenCalled();
  });
});
