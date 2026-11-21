import { describe, expect, it } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import { someQueues, t } from '../../test/fakes';
import QueueCardLine from './queue-card-line.svelte';

const printing = someQueues.printers[0];
if (printing === undefined) throw new Error('the fake overview has printers');

describe("the queue's line on a printer card", () => {
  it('says what the queue prints and how many files wait', async () => {
    await render(QueueCardLine, { summary: printing, t });

    await expect
      .element(page.getByTestId('queue-card-line'))
      .toHaveTextContent('Queue: printing benchy.bgcode, 3 more waiting');
  });

  // A paused print reads as paused, as the printer's own badge on the card.
  it('says the print is paused when the printer paused it', async () => {
    await render(QueueCardLine, {
      summary: {
        ...printing,
        printer: { ...printing.printer, state: 'PAUSED', paused: true },
      },
      t,
    });

    await expect
      .element(page.getByTestId('queue-card-line'))
      .toHaveTextContent('Queue: paused benchy.bgcode, 3 more waiting');
  });
});
