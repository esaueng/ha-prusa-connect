import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';

import { t, waitingQueue } from '../../test/fakes';
import WaitingList from './waiting-list.svelte';

const entries = waitingQueue.entries.filter(
  (entry) => entry.state === 'waiting',
);

// Drags one row onto another, as a mouse does: the drag starts on the first,
// passes over the second at the given height, and drops there.
const drag = (from: Element, onto: Element, lower: boolean): void => {
  const data = new DataTransfer();
  from.dispatchEvent(
    new DragEvent('dragstart', { bubbles: true, dataTransfer: data }),
  );
  const box = onto.getBoundingClientRect();
  const clientY = lower ? box.bottom - 2 : box.top + 2;
  onto.dispatchEvent(
    new DragEvent('dragover', {
      bubbles: true,
      cancelable: true,
      clientY,
      dataTransfer: data,
    }),
  );
  onto.dispatchEvent(
    new DragEvent('drop', {
      bubbles: true,
      cancelable: true,
      clientY,
      dataTransfer: data,
    }),
  );
};

describe('the waiting files', () => {
  // The list may be read again during a drag. The file the person picked up
  // is the one that moves, even when a row above it went away meanwhile.
  it('moves the file that was picked up when the list changed during the drag', async () => {
    if (entries.length < 3) throw new Error('the fake queue has three files');
    const onMove = vi.fn();
    const props = {
      entries,
      t,
      me: undefined,
      mayChange: true,
      limit: 30,
      onMove,
      onRemove: () => undefined,
    };
    const screen = await render(WaitingList, props);
    const rows = (): Element[] =>
      Array.from(
        screen.container.querySelectorAll('[data-part="waiting-entry"]'),
      );
    const picked = entries[1];
    const data = new DataTransfer();
    rows()[1]!.dispatchEvent(
      new DragEvent('dragstart', { bubbles: true, dataTransfer: data }),
    );

    await screen.rerender({ ...props, entries: entries.slice(1) });
    const onto = rows()[1]!;
    const clientY = onto.getBoundingClientRect().bottom - 2;
    for (const type of ['dragover', 'drop']) {
      onto.dispatchEvent(
        new DragEvent(type, {
          bubbles: true,
          cancelable: true,
          clientY,
          dataTransfer: data,
        }),
      );
    }

    expect(onMove).toHaveBeenLastCalledWith(picked?.id, 1);
  });

  it('moves a file to where it is dropped with the mouse', async () => {
    if (entries.length < 3) throw new Error('the fake queue has three files');
    const onMove = vi.fn();
    const screen = await render(WaitingList, {
      entries,
      t,
      me: undefined,
      mayChange: true,
      limit: 30,
      onMove,
      onRemove: () => undefined,
    });
    const rows = (): Element[] =>
      Array.from(
        screen.container.querySelectorAll('[data-part="waiting-entry"]'),
      );

    // The first dropped below the third lands last, in place 2.
    drag(rows()[0]!, rows()[2]!, true);
    expect(onMove).toHaveBeenLastCalledWith(entries[0]?.id, 2);

    // The third dropped above the first lands first.
    drag(rows()[2]!, rows()[0]!, false);
    expect(onMove).toHaveBeenLastCalledWith(entries[2]?.id, 0);

    // Dropped where it already is, nothing moves.
    onMove.mockClear();
    drag(rows()[1]!, rows()[0]!, true);
    expect(onMove).not.toHaveBeenCalled();
  });

  // The grip stays while a move is saved: a grip that went away and came back
  // moved every row by its width.
  it('keeps the grip while a move is saved, and drags nothing then', async () => {
    const screen = await render(WaitingList, {
      entries,
      t,
      me: undefined,
      mayChange: true,
      busy: true,
      limit: 30,
      onMove: () => undefined,
      onRemove: () => undefined,
    });

    const first = screen.container.querySelector('[data-part="waiting-entry"]');
    expect(first?.getAttribute('draggable')).not.toBe('true');
    expect(
      screen.container.querySelector('[data-part="drag-handle"]'),
    ).not.toBeNull();
  });

  it('offers no dragging to a person who may not change the queue', async () => {
    const screen = await render(WaitingList, {
      entries,
      t,
      me: undefined,
      mayChange: false,
      limit: 30,
      onMove: () => undefined,
      onRemove: () => undefined,
    });

    const first = screen.container.querySelector('[data-part="waiting-entry"]');
    expect(first?.getAttribute('draggable')).not.toBe('true');
    expect(
      screen.container.querySelector('[data-part="drag-handle"]'),
    ).toBeNull();
  });
});
