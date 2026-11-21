import { describe, expect, it } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import { t } from '../../i18n';
import SendsIndicator from './sends-indicator.svelte';
import type { SendsIndicatorRow, SendsSource } from './types';

const source = (
  rows: SendsIndicatorRow[],
): SendsSource & { cancelled: string[] } => {
  const cancelled: string[] = [];
  return {
    rows,
    active: rows.length > 0,
    uploading: rows.some((row) => row.step === 'uploading'),
    cancel: (id) => {
      cancelled.push(id);
    },
    cancelled,
  };
};

const uploading: SendsIndicatorRow = {
  id: 'job:1',
  deviceId: 'p1',
  printerName: 'Printer One',
  name: 'cube.bgcode',
  step: 'uploading',
  progress: 0.4,
  mine: true,
  startedBy: undefined,
  canCancel: true,
  stopping: false,
};

// A file somebody else started, which the person may watch and may not stop.
const theirs: SendsIndicatorRow = {
  id: 'transfer:t-9',
  deviceId: 'p2',
  printerName: 'Printer Two',
  name: 'bracket.bgcode',
  step: 'sending',
  progress: 0.5,
  mine: false,
  startedBy: undefined,
  canCancel: false,
  stopping: false,
};

const openList = async (count: number): Promise<void> => {
  await page
    .getByRole('button', { name: t('sendsIndicator.trigger', { count }) })
    .click();
};

describe('SendsIndicator', () => {
  it('draws nothing while no file was sent', async () => {
    await render(SendsIndicator, { sends: source([]) });

    await expect
      .element(
        page.getByRole('button', {
          name: t('sendsIndicator.trigger', { count: 0 }),
        }),
      )
      .not.toBeInTheDocument();
  });

  it('opens a list of the files on their way, each naming its printer with a bar and a link to it', async () => {
    await render(SendsIndicator, { sends: source([uploading]) });

    await page
      .getByRole('button', { name: t('sendsIndicator.trigger', { count: 1 }) })
      .click();

    await expect.element(page.getByText('cube.bgcode')).toBeVisible();
    await expect
      .element(
        page.getByRole('progressbar', {
          name: t('sendsIndicator.label', {
            name: 'cube.bgcode',
            printer: 'Printer One',
          }),
        }),
      )
      .toHaveAttribute('aria-valuenow', '40');
    await expect
      .element(page.getByRole('link', { name: 'Printer One' }))
      .toHaveAttribute('href', '/printers/p1/files');
  });

  it('cancels a file still going up', async () => {
    const sends = source([uploading]);
    await render(SendsIndicator, { sends });

    await page
      .getByRole('button', { name: t('sendsIndicator.trigger', { count: 1 }) })
      .click();
    await page
      .getByRole('button', {
        name: t('sendsIndicator.cancelFor', { name: 'cube.bgcode' }),
      })
      .click();

    expect(sends.cancelled).toEqual(['job:1']);
  });

  it('says a file waits for the answer about a name the printer holds, with no cancel', async () => {
    await render(SendsIndicator, {
      sends: source([
        { ...uploading, step: 'asking', progress: null, canCancel: false },
      ]),
    });

    await openList(1);
    await expect
      .element(page.getByText(t('sendsIndicator.asking')))
      .toBeVisible();
    expect(
      page
        .getByRole('button', {
          name: t('sendsIndicator.cancelFor', { name: 'cube.bgcode' }),
        })
        .elements(),
    ).toHaveLength(0);
  });

  it('says a file waits for its printer, and cancels it', async () => {
    const sends = source([{ ...uploading, step: 'waiting', progress: null }]);
    await render(SendsIndicator, { sends });

    await openList(1);
    await expect
      .element(page.getByText(t('sendsIndicator.waiting')))
      .toBeVisible();
    await page
      .getByRole('button', {
        name: t('sendsIndicator.cancelFor', { name: 'cube.bgcode' }),
      })
      .click();

    expect(sends.cancelled).toEqual(['job:1']);
  });

  it('says who started a file somebody else sent, and offers no stop to a person who may not stop it', async () => {
    await render(SendsIndicator, {
      sends: source([{ ...theirs, startedBy: 'user:ada' }]),
    });

    await openList(1);

    await expect
      .element(page.getByText(t('sendsIndicator.startedBy', { person: 'ada' })))
      .toBeVisible();
    await expect
      .element(
        page.getByRole('button', {
          name: t('sendsIndicator.stopFor', {
            name: 'bracket.bgcode',
            printer: 'Printer Two',
          }),
        }),
      )
      .not.toBeInTheDocument();
  });

  it('says somebody else started a file when the server does not name the person', async () => {
    await render(SendsIndicator, { sends: source([theirs]) });

    await openList(1);

    await expect
      .element(page.getByText(t('sendsIndicator.startedBySomeone')))
      .toBeVisible();
  });

  it('names nobody on a file the person started', async () => {
    await render(SendsIndicator, {
      sends: source([{ ...uploading, step: 'sending' }]),
    });

    await openList(1);

    await expect
      .element(page.getByText(t('sendsIndicator.startedBySomeone')))
      .not.toBeInTheDocument();
  });

  it('stops a file on its way to the printer when the person may stop it, whoever started it', async () => {
    const sends = source([uploading, { ...theirs, canCancel: true }]);
    await render(SendsIndicator, { sends });

    await openList(2);
    await page
      .getByRole('button', {
        name: t('sendsIndicator.stopFor', {
          name: 'bracket.bgcode',
          printer: 'Printer Two',
        }),
      })
      .click();

    expect(sends.cancelled).toEqual(['transfer:t-9']);
  });

  it('holds the stop button while the stop has not answered', async () => {
    await render(SendsIndicator, {
      sends: source([{ ...theirs, canCancel: true, stopping: true }]),
    });

    await openList(1);

    await expect
      .element(
        page.getByRole('button', {
          name: t('sendsIndicator.stopFor', {
            name: 'bracket.bgcode',
            printer: 'Printer Two',
          }),
        }),
      )
      .toBeDisabled();
  });

  it('asks before the tab is closed while a file is going up, and not after', async () => {
    const { rerender } = await render(SendsIndicator, {
      sends: source([uploading]),
    });

    const asked = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(asked);
    expect(asked.defaultPrevented).toBe(true);

    await rerender({ sends: source([{ ...uploading, step: 'sending' }]) });
    const free = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(free);
    expect(free.defaultPrevented).toBe(false);
  });

  it('the trigger is gone once the source holds no row', async () => {
    const { rerender } = await render(SendsIndicator, {
      sends: source([uploading]),
    });

    await rerender({ sends: source([]) });

    await expect
      .element(
        page.getByRole('button', {
          name: t('sendsIndicator.trigger', { count: 0 }),
        }),
      )
      .not.toBeInTheDocument();
  });
});
