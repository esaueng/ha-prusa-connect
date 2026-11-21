import { describe, expect, it } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import { boardView, t } from '../../test/fakes';
import QueuePage from './queue-page.svelte';

describe('the Print queue page', () => {
  // The routes a script reaches the queues with are the core's API reference
  // of this module, opened beside the page.
  it("links to the module's routes in the API reference", async () => {
    await render(QueuePage, {
      t,
      hrefOf: (device: string) => `/printers/${device}`,
      view: boardView(),
      referenceHref: '/docs?module=queue',
    });

    const link = page.getByRole('link', { name: 'Queue API' });
    await expect.element(link).toHaveAttribute('href', '/docs?module=queue');
    await expect.element(link).toHaveAttribute('target', '_blank');
  });

  // The page draws the queues the way the person last chose, and hands a new
  // choice back to be kept for them.
  it('switches between the timeline and the cards', async () => {
    const chosen: string[] = [];
    const screen = await render(QueuePage, {
      t,
      hrefOf: (device: string) => `/printers/${device}`,
      view: boardView(),
      mode: 'timeline',
      onMode: (mode: string) => chosen.push(mode),
    });

    await expect.element(page.getByTestId('board-timeline')).toBeVisible();
    await page.getByRole('radio', { name: 'Cards' }).click();
    expect(chosen).toEqual(['cards']);

    await screen.rerender({ mode: 'cards' });
    await expect.element(page.getByTestId('board-cards')).toBeVisible();
    expect(page.getByTestId('board-timeline').elements()).toHaveLength(0);
  });
});
