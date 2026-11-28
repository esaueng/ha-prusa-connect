import { page } from 'vitest/browser';
import { expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { STLLoader } from 'three/addons/loaders/STLLoader.js';

import { t } from '../../test/fakes';
import { readModel } from './model';
import {
  assembledFile,
  bed,
  loadFixture,
  placedJob,
  testPrepare,
} from './prepare-fixtures';
import PreparePanel from './prepare-panel.svelte';

it('saves the same placed model the editor shows and queues it with the profile', async () => {
  const model = vi.fn((...args: Parameters<typeof testPrepare.read>) =>
    testPrepare.read(...args),
  );
  const save = vi.fn((...args: Parameters<typeof testPrepare.save>) =>
    testPrepare.save(...args),
  );
  const submit = vi.fn((...args: Parameters<typeof testPrepare.submit>) =>
    testPrepare.submit(...args),
  );
  const onSliced = vi.fn();

  await page.viewport(1280, 840);
  await render(PreparePanel, {
    props: {
      api: { ...testPrepare, read: model, save, submit },
      beds: { 'henry-petg': bed },
      source: 'local:parts/bracket.stl',
      hasSlicer: true,
      t,
      onSliced,
    },
  });

  const slice = page.getByRole('button', { name: 'Save and slice' });
  await expect.element(slice).toBeEnabled();
  expect(model).toHaveBeenCalledWith(
    'local:parts/bracket.stl',
    expect.any(AbortSignal),
  );
  await expect.element(page.getByText('250 × 220 × 270 mm')).toBeVisible();
  const top = page.getByRole('button', { name: 'Top view' });
  await expect.element(top).toBeEnabled();
  await top.click();

  const x = page.getByLabelText('X (mm)', { exact: true });
  await x.fill('-100');
  await page.getByLabelText('Y (mm)', { exact: true }).click();
  await expect
    .element(page.getByText('Move or scale the model to fit the print volume.'))
    .toBeVisible();
  await expect.element(slice).toBeDisabled();

  await x.fill('50');
  await page.getByLabelText('Y (mm)', { exact: true }).fill('75');
  await page.getByLabelText('Rotate Z (°)', { exact: true }).fill('90');
  const scale = page.getByLabelText('Scale (%)', { exact: true });
  await scale.fill('150');
  await page.getByRole('heading', { name: 'Placement' }).click();

  await expect.element(slice).toBeEnabled();
  await slice.click();
  await expect.poll(() => onSliced.mock.calls).toEqual([[placedJob]]);

  const [storage, name, body] = save.mock.calls[0] ?? [];
  expect(storage).toBe('local');
  expect(name).toMatch(/^bracket-placed-.+\.stl$/u);
  expect(submit).toHaveBeenCalledWith(
    'local',
    name,
    'henry-petg',
    expect.any(AbortSignal),
  );
  if (!body) throw new Error('No model was saved');

  const written = new STLLoader().parse(await body.arrayBuffer());
  written.computeBoundingBox();
  expect(written.boundingBox?.min.toArray()).toEqual([35, 67.5, 0]);
  expect(written.boundingBox?.max.toArray()).toEqual([65, 82.5, 45]);
  written.dispose();
  const reopened = await readModel(new File([body], name ?? 'placed.stl'));
  expect(reopened.placedAt).toEqual([50, 75]);
  reopened.geometry.dispose();
});

it('keeps the empty editor usable on a narrow screen', async () => {
  await page.viewport(390, 844);
  await render(PreparePanel, {
    props: {
      api: testPrepare,
      hasSlicer: true,
      beds: { 'charlie-pla': bed },
      t,
      onSliced: vi.fn(),
    },
  });

  await expect
    .element(page.getByRole('button', { name: 'Open model' }))
    .toBeVisible();
  await expect
    .element(page.getByRole('button', { name: 'Save and slice' }))
    .toBeDisabled();
  expect(document.documentElement.scrollWidth).toBeLessThanOrEqual(390);
});

it.each(['pick', 'drop'])(
  'imports a 3MF by %s and slices its placed geometry',
  async (input) => {
    const save = vi.fn((...args: Parameters<typeof testPrepare.save>) =>
      testPrepare.save(...args),
    );
    const submit = vi.fn((...args: Parameters<typeof testPrepare.submit>) =>
      testPrepare.submit(...args),
    );
    const onSliced = vi.fn();
    await page.viewport(1280, 840);
    const { container } = await render(PreparePanel, {
      props: {
        api: { ...testPrepare, save, submit },
        beds: { 'henry-petg': bed },
        hasSlicer: true,
        t,
        onSliced,
      },
    });

    const file = await assembledFile();
    if (input === 'pick') {
      const picker =
        container.querySelector<HTMLInputElement>('input[type="file"]');
      if (!picker) throw new Error('Prepare did not render the file picker');
      await page.elementLocator(picker).upload(file);
    } else {
      const dataTransfer = new DataTransfer();
      dataTransfer.items.add(file);
      page
        .getByLabelText('3D build plate view')
        .element()
        .dispatchEvent(new DragEvent('drop', { bubbles: true, dataTransfer }));
    }

    const slice = page.getByRole('button', { name: 'Save and slice' });
    await expect.element(slice).toBeEnabled();
    await expect
      .element(page.getByText(/Apply any cuts made with negative volumes/u))
      .toBeVisible();
    const x = page.getByLabelText('X (mm)', { exact: true });
    const y = page.getByLabelText('Y (mm)', { exact: true });
    const xInput = x.element();
    const yInput = y.element();
    if (
      !(xInput instanceof HTMLInputElement) ||
      !(yInput instanceof HTMLInputElement)
    )
      throw new Error('Prepare did not render XY controls');
    expect(xInput.valueAsNumber).toBeCloseTo(96.6);
    expect(yInput.valueAsNumber).toBeCloseTo(55.8);
    await x.fill('100');
    await page.getByRole('heading', { name: 'Placement' }).click();
    await slice.click();
    await expect.poll(() => onSliced.mock.calls).toEqual([[placedJob]]);

    const [storage, name, body] = save.mock.calls[0] ?? [];
    expect(name).toMatch(/^assembled-placed-.+\.stl$/u);
    expect(submit).toHaveBeenCalledWith(
      storage,
      name,
      'henry-petg',
      expect.any(AbortSignal),
    );
    if (!body) throw new Error('No assembly was saved');
    const written = new STLLoader().parse(await body.arrayBuffer());
    expect(written.getAttribute('position').count).toBe(24);
    written.computeBoundingBox();
    const bounds = written.boundingBox;
    if (!bounds) throw new Error('The assembly has no bounds');
    expect(bounds.min.x).toBeCloseTo(69.6);
    expect(bounds.min.y).toBeCloseTo(25.4);
    expect(bounds.min.z).toBe(0);
    expect(bounds.max.x).toBeCloseTo(130.4);
    expect(bounds.max.y).toBeCloseTo(86.2);
    expect(bounds.max.z).toBeCloseTo(22.7);
    written.dispose();
  },
);

it('shows the import limit when a 3MF expands beyond the archive budget', async () => {
  const { container } = await render(PreparePanel, {
    props: {
      api: testPrepare,
      beds: { 'henry-petg': bed },
      hasSlicer: true,
      t,
      onSliced: vi.fn(),
    },
  });
  const picker =
    container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!picker) throw new Error('Prepare did not render the file picker');
  await page
    .elementLocator(picker)
    .upload(await loadFixture('large-archive.3mf'));
  await expect
    .element(page.getByText(/The model exceeds the import limits/u))
    .toBeVisible();
  await expect
    .element(page.getByRole('button', { name: 'Save and slice' }))
    .toBeDisabled();
});

it('asks for geometry only when a project has unknown part roles', async () => {
  const { container } = await render(PreparePanel, {
    props: {
      api: testPrepare,
      beds: { 'henry-petg': bed },
      hasSlicer: true,
      t,
      onSliced: vi.fn(),
    },
  });
  const picker =
    container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!picker) throw new Error('Prepare did not render the file picker');
  await page
    .elementLocator(picker)
    .upload(await loadFixture('bambu-unknown-role.3mf'));
  await expect
    .element(page.getByText(/Could not determine the 3MF part roles/u))
    .toBeVisible();
  await expect
    .element(page.getByRole('button', { name: 'Save and slice' }))
    .toBeDisabled();
});
