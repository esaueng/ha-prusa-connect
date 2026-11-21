import { describe, expect, it, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import { someBoard, t } from '../../test/fakes';
import BoardCards from './board-cards.svelte';

const props = {
  queues: someBoard,
  t,
  hrefOf: (device: string) => `/printers/${device}`,
};

// Drags one card onto another, as a mouse does: the drag starts on the first,
// passes over the second and drops on its right or left half.
const drag = (from: Element, onto: Element, right: boolean): void => {
  const data = new DataTransfer();
  from.dispatchEvent(
    new DragEvent('dragstart', { bubbles: true, dataTransfer: data }),
  );
  const box = onto.getBoundingClientRect();
  const clientX = right ? box.right - 2 : box.left + 2;
  for (const type of ['dragover', 'drop']) {
    onto.dispatchEvent(
      new DragEvent(type, {
        bubbles: true,
        cancelable: true,
        clientX,
        dataTransfer: data,
      }),
    );
  }
};

describe('the queues as cards', () => {
  it('draws a row of cards for each printer, what it prints first', async () => {
    const screen = await render(BoardCards, props);

    const rows = page
      .getByTestId('board-card-row')
      .elements()
      .map((row) => row.getAttribute('data-device'));
    expect(rows).toEqual(['mk4-1', 'core-one', 'mini']);
    const cards = Array.from(
      screen.container.querySelectorAll(
        '[data-device="mk4-1"] [data-part="board-card"]',
      ),
    ).map((card) => card.getAttribute('data-entry'));
    expect(cards).toEqual(['e0', 'e1', 'e2', 'e3']);
    // The file printing now leads the row, before the divider.
    expect(
      screen.container
        .querySelector(
          '[data-device="mk4-1"] [data-part="board-now"] [data-part="board-card"]',
        )
        ?.getAttribute('data-entry'),
    ).toBe('e0');
  });

  it('says when a printer works on nothing and nothing waits', async () => {
    const screen = await render(BoardCards, props);

    const mini = screen.container.querySelector('[data-device="mini"]')!;
    expect(mini.querySelector('[data-part="board-now-empty"]')).not.toBeNull();
    expect(
      mini.querySelector('[data-part="board-cards-empty"]'),
    ).not.toBeNull();
  });

  it("opens the printer's Queue tab from a card", async () => {
    await render(BoardCards, props);

    await expect
      .element(
        page
          .getByRole('region', { name: 'CORE One' })
          .getByRole('link', { name: 'lid-0.4n-PETG.bgcode' }),
      )
      .toHaveAttribute('href', '/printers/core-one');
  });

  it('moves a waiting file to where it is dropped in its row', async () => {
    const onMove = vi.fn();
    const screen = await render(BoardCards, { ...props, onMove });
    const card = (entry: string): Element =>
      screen.container.querySelector(
        `[data-device="mk4-1"] [data-entry="${entry}"]`,
      )!;

    // Behind the printing file wait e1, e2, e3.
    drag(card('e1'), card('e3'), true);
    expect(onMove).toHaveBeenLastCalledWith('mk4-1', 'e1', 2);
    drag(card('e3'), card('e1'), false);
    expect(onMove).toHaveBeenLastCalledWith('mk4-1', 'e3', 0);
    // The file on the printer is never dragged.
    expect(card('e0').getAttribute('draggable')).not.toBe('true');
  });

  it('offers no dragging without a way to move', async () => {
    const screen = await render(BoardCards, props);

    expect(
      screen.container
        .querySelector('[data-entry="e1"]')
        ?.getAttribute('draggable'),
    ).not.toBe('true');
  });
});
