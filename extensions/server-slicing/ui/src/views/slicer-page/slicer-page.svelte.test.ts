import { userEvent } from 'vitest/browser';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import { PageAddress } from '../../page-address.svelte';
import { DEFAULT_STATE, type PageState } from '../../page-state';
import {
  adminSlicers,
  everybodysJobs,
  installingSlicers,
  linuxSlicers,
  longJobs,
  manySlicers,
  someJobs,
  someSlicers,
  t,
  troubledSlicers,
  updateSlicers,
  viewOf,
} from '../../test/fakes';
import { bed, testPrepare } from '../prepare-panel/prepare-fixtures';
import SlicerPage from './slicer-page.svelte';

const at = (state: Partial<PageState> = {}) =>
  new PageAddress(null, { ...DEFAULT_STATE, ...state });

const row = (version: string) =>
  page
    .getByTestId('release-table')
    .getByRole('row')
    .filter({
      has: page.getByRole('link', { name: version, exact: true }),
    });

const shownVersions = () =>
  page
    .getByTestId('release-row')
    .elements()
    .map((element) => element.getAttribute('data-version'));

const menuOf = async (name: string) => {
  // A menu still closing from the last press would answer for this one.
  await expect.poll(() => page.getByRole('menu').elements()).toHaveLength(0);
  await page.getByRole('button', { name: `Actions for ${name}` }).click();
};

describe('the slicer page', () => {
  // A desktop width: below it the table folds its columns away, which the
  // phone test holds.
  beforeEach(async () => {
    await page.viewport(1280, 900);
  });

  it('lists one row per version, newest first, with its state and checksum', async () => {
    await render(SlicerPage, {
      view: viewOf('ready', adminSlicers, everybodysJobs),
      t,
      address: at(),
    });

    // The local 2.9.5 has no offer and still has its row.
    expect(shownVersions()).toEqual([
      '3.0.0-alpha12',
      '2.9.6',
      '2.9.6-rc1',
      '2.9.5',
      '2.9.2',
    ]);
    await expect.element(row('2.9.6').getByText('Default')).toBeVisible();
    await expect.element(row('2.9.6').getByText('Installed')).toBeVisible();
    await expect.element(row('2.9.2').getByText('No checksum')).toBeVisible();
    await expect
      .element(row('2.9.5').getByText('Nothing to check'))
      .toBeVisible();
  });

  it('narrows the versions by search, and says when none match', async () => {
    const address = at();
    await render(SlicerPage, {
      view: viewOf('ready', adminSlicers, everybodysJobs),
      t,
      address,
    });
    const search = page.getByRole('searchbox', { name: 'Search the versions' });

    await search.fill('2.9.6');
    expect(shownVersions()).toEqual(['2.9.6', '2.9.6-rc1']);
    expect(address.state.releases.q).toBe('2.9.6');

    await search.fill('9.9');
    await expect
      .element(page.getByText('No version matches these filters'))
      .toBeVisible();
  });

  it('keeps the filters it was opened with', async () => {
    await render(SlicerPage, {
      view: viewOf('ready', manySlicers, everybodysJobs),
      t,
      address: at({
        releases: { q: '', channel: ['rc'], state: [], line: ['2.8'] },
      }),
    });

    expect(shownVersions()).toEqual(['2.8.1-rc1', '2.8.0-rc1']);
  });

  it('orders the versions by release date when asked', async () => {
    const address = at();
    await render(SlicerPage, {
      view: viewOf('ready', adminSlicers, everybodysJobs),
      t,
      address,
    });

    await page.getByRole('button', { name: 'Released' }).click();
    expect(address.state.releaseSort).toEqual({
      column: 'released',
      ascending: false,
    });
    await page.getByRole('button', { name: 'Released' }).click();
    expect(address.state.releaseSort.ascending).toBe(true);
    expect(shownVersions()[0]).toBe('2.9.2');
  });

  it('installs from a row, and asks first when no checksum was published', async () => {
    const view = viewOf('ready', adminSlicers, everybodysJobs);
    const install = vi.spyOn(view, 'install');
    await render(SlicerPage, { view, t, address: at() });

    await menuOf('PrusaSlicer 3.0.0-alpha12');
    await page.getByRole('menuitem', { name: 'Install' }).click();
    expect(install).toHaveBeenCalledWith('3.0.0-alpha12', false);

    await menuOf('PrusaSlicer 2.9.2');
    await page.getByRole('menuitem', { name: 'Install' }).click();
    expect(install).toHaveBeenCalledTimes(1);
    await page
      .getByRole('button', { name: 'Install without a checksum' })
      .click();
    expect(install).toHaveBeenCalledWith('2.9.2', true);
  });

  it('makes a version the default and removes one only after asking', async () => {
    const view = viewOf('ready', adminSlicers, everybodysJobs);
    const pin = vi.spyOn(view, 'pin');
    const remove = vi.spyOn(view, 'remove');
    await render(SlicerPage, { view, t, address: at() });

    await menuOf('PrusaSlicer 2.9.5');
    // A local binary is the operator's, so it is never removed from here.
    expect(
      page.getByRole('menuitem', { name: 'Remove' }).elements(),
    ).toHaveLength(0);
    await page.getByRole('menuitem', { name: 'Make default' }).click();
    expect(pin).toHaveBeenCalledWith('2.9.5');

    await menuOf('PrusaSlicer 2.9.6');
    await page.getByRole('menuitem', { name: 'Remove' }).click();
    expect(remove).not.toHaveBeenCalled();
    await page.getByRole('button', { name: 'Remove', exact: true }).click();
    expect(remove).toHaveBeenCalledWith('2.9.6');
  });

  it('opens a release on a click of its row, with the download and the executable', async () => {
    const address = at();
    await render(SlicerPage, {
      view: viewOf('ready', adminSlicers, everybodysJobs),
      t,
      address,
    });

    await page.getByRole('link', { name: '2.9.6', exact: true }).click();
    expect(address.state.release).toBe('2.9.6');

    expect(
      page.getByRole('heading', { level: 1 }).element().textContent,
    ).toContain('PrusaSlicer 2.9.6');
    expect(page.getByTestId('download').element().textContent).toContain(
      'PrusaSlicer-2.9.6.dmg',
    );
    expect(page.getByTestId('download').element().textContent).toContain(
      '94fd7b8a9f87',
    );
    expect(
      page.getByTestId('installed-copy').first().element().textContent,
    ).toContain('MacOS/PrusaSlicer');
    await expect
      .element(page.getByRole('link', { name: 'Release notes' }))
      .toHaveAttribute('target', '_blank');

    await page.getByRole('link', { name: 'Slicers' }).click();
    expect(address.state.release).toBeUndefined();
    await expect.element(page.getByTestId('release-table')).toBeVisible();
  });

  it('shows how far an install is, and why one failed', async () => {
    const { rerender } = await render(SlicerPage, {
      view: viewOf('ready', installingSlicers, everybodysJobs),
      t,
      address: at(),
    });

    await expect
      .element(row('3.0.0-alpha12').getByText('Downloading'))
      .toBeVisible();
    await expect.element(row('3.0.0-alpha12').getByText('45 %')).toBeVisible();

    await rerender({
      view: viewOf('ready', troubledSlicers, everybodysJobs),
      t,
      address: at({ release: '3.0.0-alpha12' }),
    });
    expect(page.getByTestId('install-error').element().textContent).toContain(
      'hdiutil attach',
    );
    await expect
      .element(page.getByRole('button', { name: 'Try again' }))
      .toBeVisible();
  });

  it('shows a release this server cannot install, with the reason', async () => {
    await render(SlicerPage, {
      view: viewOf('ready', linuxSlicers, everybodysJobs),
      t,
      address: at({ release: '2.9.6' }),
    });

    await expect
      .element(page.getByText('Not offered here: no build for linux/arm64'))
      .toBeVisible();
    expect(
      page.getByRole('button', { name: 'Install' }).elements(),
    ).toHaveLength(0);
  });

  it('says how to get a build when the newest release has none here', async () => {
    await render(SlicerPage, {
      view: viewOf('ready', linuxSlicers, everybodysJobs),
      t,
      address: at(),
    });

    await expect
      .element(page.getByText(/an operator names a manifest/u))
      .toBeVisible();
  });

  it('gives a reader the lists and nothing to change', async () => {
    await render(SlicerPage, {
      view: viewOf('ready', someSlicers, someJobs),
      t,
      address: at(),
    });

    await menuOf('PrusaSlicer 2.9.2');
    expect(page.getByRole('menuitem').elements()).toHaveLength(1);
    await expect
      .element(page.getByRole('menuitem', { name: 'Show details' }))
      .toBeVisible();
    await userEvent.keyboard('{Escape}');
  });

  it('offers a newer final release and installs it only when asked', async () => {
    const view = viewOf('ready', updateSlicers, everybodysJobs);
    const install = vi.spyOn(view, 'install');

    await render(SlicerPage, { view, t, address: at() });

    await expect
      .element(page.getByText('PrusaSlicer 2.9.7 is available'))
      .toBeVisible();
    expect(install).not.toHaveBeenCalled();
    await page.getByRole('button', { name: 'Install 2.9.7' }).click();
    expect(install).toHaveBeenCalledWith('2.9.7', false);
  });

  it('lists the jobs on their own tab, searches them and cancels one', async () => {
    const view = viewOf('ready', adminSlicers, everybodysJobs);
    const cancel = vi.spyOn(view, 'cancel');
    const address = at();
    await render(SlicerPage, { view, t, address });

    await page.getByRole('tab', { name: /Jobs/u }).click();
    expect(address.state.tab).toBe('jobs');
    expect(page.getByTestId('job-row').elements()).toHaveLength(4);

    const search = page.getByRole('searchbox', { name: 'Search the jobs' });
    await search.fill('gear');
    expect(page.getByTestId('job-row').elements()).toHaveLength(1);
    await search.fill('');

    await page
      .getByRole('button', { name: 'Actions for brackets.3mf' })
      .first()
      .click();
    await page.getByRole('menuitem', { name: 'Cancel' }).click();
    expect(cancel).toHaveBeenCalledWith('a1');
  });

  it('opens a failed job with the end of what PrusaSlicer printed', async () => {
    const address = at({ tab: 'jobs' });
    await render(SlicerPage, {
      view: viewOf('ready', adminSlicers, longJobs),
      t,
      address,
    });

    await page
      .getByRole('link', { name: /a-project-with-a-name-far-too-long/u })
      .click();
    expect(address.state.job).toBe('b1');
    expect(page.getByTestId('job-error').element().textContent).toContain(
      'PrusaSlicer stopped with an error',
    );
    expect(page.getByTestId('log').element().textContent).toContain(
      'too tall and cannot be printed',
    );
  });

  it('opens a finished job with its G-code', async () => {
    await render(SlicerPage, {
      view: viewOf('ready', adminSlicers, everybodysJobs),
      t,
      address: at({ job: 'a3' }),
    });

    await expect
      .element(page.getByRole('link', { name: 'Open the G-code' }))
      .toHaveAttribute('href', '/storage/local/file/plates%2Fbrackets.bgcode');
    expect(page.getByTestId('output').element().textContent).toContain(
      'brackets.bgcode',
    );
  });

  it('says a job or a release it does not hold is not listed', async () => {
    const { rerender } = await render(SlicerPage, {
      view: viewOf('ready', adminSlicers, everybodysJobs),
      t,
      address: at({ job: 'gone' }),
    });
    await expect
      .element(page.getByText('This job is not listed'))
      .toBeVisible();

    await rerender({
      view: viewOf('ready', adminSlicers, everybodysJobs),
      t,
      address: at({ release: '1.0.0' }),
    });
    await expect
      .element(page.getByText('This version is not listed'))
      .toBeVisible();
  });

  it('never scrolls sideways at the width of a phone', async () => {
    await page.viewport(390, 844);
    try {
      for (const state of [
        {},
        { tab: 'jobs' as const },
        { job: 'b1' },
        { release: '2.9.6' },
        { tab: 'api' as const },
      ]) {
        const screen = await render(SlicerPage, {
          view: viewOf('ready', adminSlicers, longJobs),
          t,
          address: at(state),
        });
        await expect.element(page.getByTestId('slicer-page')).toBeVisible();
        const root = document.scrollingElement!;
        expect(root.scrollWidth, JSON.stringify(state)).toBeLessThanOrEqual(
          root.clientWidth,
        );
        await screen.unmount();
      }
    } finally {
      await page.viewport(1280, 900);
    }
  });

  it('titles the page Slicing above its tabs', async () => {
    await render(SlicerPage, {
      view: viewOf('ready', adminSlicers, everybodysJobs),
      t,
      address: at(),
    });

    const heading = page.getByRole('heading', { level: 1 });
    expect(heading.element().textContent).toBe('Slicing');
    expect(heading.element().classList.contains('sr-only')).toBe(false);
  });

  it('documents the routes on the API tab, with the address of this server', async () => {
    const address = at();
    await render(SlicerPage, {
      view: viewOf('ready', adminSlicers, everybodysJobs),
      t,
      address,
      apiBase: 'http://printers.local:8080/api/v1/modules/ns',
    });

    await page.getByRole('tab', { name: 'API' }).click();
    expect(address.state.tab).toBe('api');
    await expect
      .element(page.getByRole('textbox', { name: 'Base address' }))
      .toHaveValue('http://printers.local:8080/api/v1/modules/ns');
    await expect
      .element(page.getByRole('link', { name: 'Settings, API credentials' }))
      .toHaveAttribute('href', '/settings/tokens');
    // The routes are the core's reference page, showing this module's document
    // and told it is framed, which a browser on plain HTTP does not say.
    expect(page.getByTestId('api-viewer').element().getAttribute('src')).toBe(
      '/docs?module=com.github.aux.prusa3d.server-slicing&embedded=1',
    );
    expect(page.getByTestId('api-scopes').element().textContent).toContain(
      'POST /slicers/install',
    );
    expect(page.getByTestId('api-example').element().textContent).toContain(
      '/jobs/upload?name=plate.3mf',
    );
    expect(page.getByTestId('api-failures').element().textContent).toContain(
      'server-slicing/NOT_PRUSASLICER',
    );
  });

  it('says a refused person may not see the module', async () => {
    await render(SlicerPage, { view: viewOf('refused'), t });

    await expect
      .element(page.getByText("You may not see this module's slicers and jobs"))
      .toBeVisible();
  });
  // A file's Slice button opens the page on the project. Sending the dialog
  // queues the job and opens it.
  it('slices the project the address names and opens its job', async () => {
    const submit = vi.fn(() => Promise.resolve('j9'));
    const address = at({ slice: 'local:projekty/box.3mf' });
    await render(SlicerPage, {
      props: {
        view: { ...viewOf('ready', someSlicers, someJobs), submit },
        t,
        address,
      },
    });

    const dialog = page.getByRole('dialog');
    await expect.element(dialog.getByText(/box\.3mf/)).toBeVisible();
    await dialog.getByRole('button', { name: 'Slice' }).click();

    expect(submit).toHaveBeenCalledWith({
      source: { storage: 'local', path: 'projekty/box.3mf' },
      selection: { setup: 0, bed: 0 },
      format: '',
      overwrite: false,
    });
    await expect.poll(() => address.state.slice).toBeUndefined();
    expect(address.state).toMatchObject({ tab: 'jobs', job: 'j9' });
  });
  it('keeps the slice dialog open and says why the server refused', async () => {
    const address = at({ slice: 'local:box.3mf' });
    const view = {
      ...viewOf('ready', someSlicers, someJobs),
      actionFault: {
        title: 'No PrusaSlicer is installed',
        detail: '',
        code: 'server-slicing/NO_SLICER',
      },
      submit: vi.fn(() => Promise.resolve(undefined)),
    };
    await render(SlicerPage, { props: { view, t, address } });

    const dialog = page.getByRole('dialog');
    await dialog.getByRole('button', { name: 'Slice' }).click();

    await expect
      .element(dialog.getByText('No PrusaSlicer is installed'))
      .toBeVisible();
    expect(address.state.slice).toBe('local:box.3mf');
  });

  it('opens a model from Storage in Prepare and navigates to its slicing job', async () => {
    const address = at({ tab: 'prepare', model: 'local:parts/bracket.stl' });
    await render(SlicerPage, {
      view: viewOf(
        'ready',
        {
          ...adminSlicers,
          prepare_beds: { 'core-one-petg': bed },
        },
        everybodysJobs,
      ),
      t,
      address,
      prepare: testPrepare,
    });

    await expect
      .element(page.getByRole('tab', { name: 'Prepare' }))
      .toHaveAttribute('data-state', 'active');
    await expect
      .element(page.getByText('bracket.stl', { exact: true }))
      .toBeVisible();
    const save = page.getByRole('button', { name: 'Save and slice' });
    await expect.element(save).toBeEnabled();
    await save.click();
    await expect
      .poll(() => address.state)
      .toMatchObject({ tab: 'jobs', job: 'placed-job', model: undefined });
    await expect
      .element(page.getByText('This job is not listed'))
      .not.toBeInTheDocument();
    await expect
      .element(page.getByText('local: bracket-placed.stl', { exact: true }))
      .toBeVisible();
  });

  it('requires a saved profile and sends its name with an STL job', async () => {
    const submit = vi.fn(() => Promise.resolve('j9'));
    const address = at({ slice: 'local:models/box.STL' });
    await render(SlicerPage, {
      props: {
        view: {
          ...viewOf(
            'ready',
            { ...someSlicers, stl_profiles: ['core-one-petg'] },
            someJobs,
          ),
          submit,
        },
        t,
        address,
      },
    });

    const dialog = page.getByRole('dialog');
    const slice = dialog.getByRole('button', { name: 'Slice' });
    await expect
      .element(dialog.getByRole('heading', { name: 'Slice the STL' }))
      .toBeVisible();
    await expect.element(slice).toBeDisabled();
    await expect
      .element(dialog.getByText('Bed', { exact: true }))
      .not.toBeInTheDocument();
    await dialog.getByRole('combobox', { name: 'STL profile' }).click();
    await page.getByRole('option', { name: 'core-one-petg' }).click();
    await slice.click();

    expect(submit).toHaveBeenCalledWith({
      source: { storage: 'local', path: 'models/box.STL' },
      profile: 'core-one-petg',
      format: '',
      overwrite: false,
    });
    expect(address.state).toMatchObject({ tab: 'jobs', job: 'j9' });
  });
});
