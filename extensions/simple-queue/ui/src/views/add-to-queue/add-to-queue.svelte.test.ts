import { describe, expect, it, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import type { Added } from '../../adding';
import { t } from '../../test/fakes';
import AddToQueue from './add-to-queue.svelte';

describe('the print dialog section', () => {
  it('asks for a printer while none is ticked', async () => {
    await render(AddToQueue, { printers: [], t, onAdd: vi.fn() });

    await expect
      .element(page.getByRole('button', { name: 'Add to the queue' }))
      .toBeDisabled();
    expect(page.getByTestId('add-to-queue').element().textContent).toContain(
      'Choose printers above',
    );
  });

  it('adds to every ticked printer and says how each ended', async () => {
    const onAdd = vi.fn((device: string): Promise<Added> =>
      Promise.resolve(
        {
          a: { kind: 'added', position: 2 } as const,
          b: { kind: 'started' } as const,
          c: {
            kind: 'failed',
            fault: {
              code: undefined,
              title: 'The queue is full',
              detail: undefined,
            },
          } as const,
        }[device] ?? {
          kind: 'failed',
          fault: { code: undefined, title: '', detail: undefined },
        },
      ),
    );
    await render(AddToQueue, {
      printers: [
        { id: 'a', name: 'MK4' },
        { id: 'b', name: 'MINI' },
        { id: 'c', name: 'XL' },
      ],
      t,
      onAdd,
    });

    await page.getByRole('button', { name: 'Add to 3 queues' }).click();

    expect(onAdd.mock.calls.map(([device]) => device)).toEqual(['a', 'b', 'c']);
    const lines = page
      .getByTestId('added')
      .elements()
      .map((element) => element.textContent.trim());
    expect(lines).toEqual([
      'MK4: number 2 in the queue',
      'MINI: printing now',
      'XL: The queue is full',
    ]);
  });
});
