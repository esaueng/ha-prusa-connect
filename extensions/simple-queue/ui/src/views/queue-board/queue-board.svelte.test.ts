import { describe, expect, it } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import { boardView, t } from '../../test/fakes';
import QueueBoard from './queue-board.svelte';

const props = {
  t,
  hrefOf: (device: string) => `/printers/${device}`,
};

describe('the Print queue page', () => {
  it('draws every printer as a lane of one time axis, each beside its printer', async () => {
    const screen = await render(QueueBoard, { ...props, view: boardView() });

    await expect.element(page.getByTestId('board-timeline')).toBeVisible();
    const printers = page
      .getByTestId('board-printer')
      .elements()
      .map((row) => row.getAttribute('data-device'));
    const lanes = Array.from(
      screen.container.querySelectorAll('[data-part="timeline-lane"]'),
    ).map((lane) => lane.getAttribute('data-lane'));
    expect(printers).toEqual(['mk4-1', 'core-one', 'mini']);
    expect(lanes).toEqual(printers);
    // Each printer is drawn beside its name, as the printers list draws it.
    const pictures = Array.from(
      screen.container.querySelectorAll(
        '[data-part="board-printer"] [data-slot="printer-icon"]',
      ),
    ).map((icon) => icon.getAttribute('data-variant'));
    expect(pictures).toEqual(['mk4s', 'core_one', 'mini']);
    // A printer's name opens its Queue tab, where its files are listed.
    await expect
      .element(page.getByRole('link', { name: 'CORE One' }))
      .toHaveAttribute('href', '/printers/core-one');
    // An empty queue is a lane with no bar.
    expect(
      screen.container.querySelectorAll(
        '[data-lane="mini"] [data-part="timeline-slot"]',
      ),
    ).toHaveLength(0);
  });

  it('draws an hour wider when zoomed in, on every lane at once', async () => {
    const screen = await render(QueueBoard, { ...props, view: boardView() });
    const width = (): number =>
      screen.container
        .querySelector('[data-part="timeline-slot"]')!
        .getBoundingClientRect().width;

    await expect.element(page.getByTestId('board-timeline')).toBeVisible();
    const before = width();
    await page.getByRole('button', { name: 'Zoom in (Ctrl + wheel)' }).click();
    await expect.poll(width).toBeGreaterThan(before);
  });

  it('puts the zoom back and the axis at now with Back to now', async () => {
    const screen = await render(QueueBoard, { ...props, view: boardView() });
    const width = (): number =>
      screen.container
        .querySelector('[data-part="timeline-slot"]')!
        .getBoundingClientRect().width;
    const scroller = (): HTMLElement =>
      screen.container.querySelector<HTMLElement>(
        '[data-part="timeline-scroll"]',
      )!;

    await expect.element(page.getByTestId('board-timeline')).toBeVisible();
    const before = width();
    await page.getByRole('button', { name: 'Zoom in (Ctrl + wheel)' }).click();
    await expect.poll(width).toBeGreaterThan(before);
    scroller().scrollLeft = 300;

    await page.getByRole('button', { name: 'Back to now' }).click();
    await expect.poll(width).toBe(before);
    await expect.poll(() => scroller().scrollLeft).toBe(0);
  });

  it('zooms on a pinch of the touchpad, which the browser sends as Ctrl and the wheel', async () => {
    const screen = await render(QueueBoard, { ...props, view: boardView() });
    const width = (): number =>
      screen.container
        .querySelector('[data-part="timeline-slot"]')!
        .getBoundingClientRect().width;

    await expect.element(page.getByTestId('board-timeline')).toBeVisible();
    const before = width();
    const pinch = new WheelEvent('wheel', {
      deltaY: -120,
      ctrlKey: true,
      cancelable: true,
    });
    screen.container
      .querySelector('[data-part="timeline-scroll"]')!
      .dispatchEvent(pinch);

    await expect.poll(width).toBeGreaterThan(before);
    // The page itself is not zoomed.
    expect(pinch.defaultPrevented).toBe(true);
  });

  // Firefox counts a mouse wheel in lines, three to a notch, which a sum of
  // pixels would take many notches to reach.
  it('zooms one step on one notch of the mouse wheel held with Ctrl', async () => {
    const screen = await render(QueueBoard, { ...props, view: boardView() });
    const width = (): number =>
      screen.container
        .querySelector('[data-part="timeline-slot"]')!
        .getBoundingClientRect().width;

    await expect.element(page.getByTestId('board-timeline')).toBeVisible();
    const before = width();
    screen.container
      .querySelector('[data-part="timeline-scroll"]')!
      .dispatchEvent(
        new WheelEvent('wheel', {
          deltaY: -3,
          deltaMode: WheelEvent.DOM_DELTA_LINE,
          ctrlKey: true,
          cancelable: true,
        }),
      );

    await expect.poll(width).toBeGreaterThan(before);
  });

  it('leaves the wheel without Ctrl to scroll the page', async () => {
    const screen = await render(QueueBoard, { ...props, view: boardView() });

    await expect.element(page.getByTestId('board-timeline')).toBeVisible();
    const scroll = new WheelEvent('wheel', { deltaY: -120, cancelable: true });
    screen.container
      .querySelector('[data-part="timeline-scroll"]')!
      .dispatchEvent(scroll);

    expect(scroll.defaultPrevented).toBe(false);
  });
});
