import { describe, expect, it, vi } from 'vitest';
import { page } from 'vitest/browser';
import { render } from 'vitest-browser-svelte';

import type { FileAt } from '../../adding';
import { FilePicker } from '../../file-picker.svelte';
import {
  ME,
  emptyQueue,
  fakeFilesApi,
  heldQueue,
  manualQueue,
  onPrinterQueue,
  printingQueue,
  queueView,
  readOnlyQueue,
  readyQueue,
  refusal,
  sendingQueue,
  t,
  waitingQueue,
} from '../../test/fakes';
import PrinterQueue from './printer-queue.svelte';

describe('the Queue tab', () => {
  it('lists the waiting files in order, and the print that runs', async () => {
    await render(PrinterQueue, { view: queueView(printingQueue), t, me: ME });

    await expect
      .poll(() => page.getByTestId('active-entry').element().textContent)
      .toContain('benchy.bgcode');
    await expect
      .poll(() => page.getByTestId('active-entry').element().textContent)
      .toContain('Started by the queue');
    const names = page
      .getByTestId('waiting-entry')
      .elements()
      .map((element) => element.getAttribute('data-entry'));
    expect(names).toEqual(['e1', 'e2', 'e3']);
    await expect
      .poll(() => page.getByTestId('waiting-list').element().textContent)
      .toContain('Added by bob');
    // What ended is one row until it is opened. The row says how the last
    // print did, and the list under it who queued and started each one.
    await expect
      .poll(() => page.getByTestId('history-last').element().textContent)
      .toContain('Last:');
    await expect
      .element(page.getByTestId('history-entry').first())
      .not.toBeVisible();
    await page.getByRole('button', { name: /^Ended/u }).click();
    await expect
      .poll(() => page.getByTestId('history-list').element().textContent)
      .toContain('Failed');
    await expect
      .poll(() => page.getByTestId('history-list').element().textContent)
      .toContain('Started by');
  });

  it('names under a file what its slicer wrote, and leaves out what it did not', async () => {
    const queue = {
      ...waitingQueue,
      entries: waitingQueue.entries.map((entry, index) =>
        index === 0
          ? {
              ...entry,
              printer_model: 'XL2IS',
              layer_height_mm: 0.2,
              material: 'PLA;PETG',
              size: 4_760_000,
            }
          : entry,
      ),
    };
    await render(PrinterQueue, { view: queueView(queue), t, me: ME });

    const facts = page.getByTestId('entry-facts');
    await expect.element(facts.first()).toBeVisible();
    // The other files name none of what the slicer wrote.
    expect(
      facts
        .elements()
        .filter((row) => row.querySelector('[data-fact="printer"]')),
    ).toHaveLength(1);
    const text = facts.first().element().textContent;
    for (const part of [
      'Printer type',
      'XL2IS',
      '0.2 mm',
      'PLA, PETG',
      '4.5 MB',
    ]) {
      expect(text).toContain(part);
    }
  });

  it('lays the queue out in time: a bar, and the times of each file', async () => {
    // 09:35 local time, held still. One print runs with 67 minutes left, the
    // first waiting file names two hours and the second names nothing.
    const now = new Date(2026, 10, 13, 9, 35).getTime();
    let waiting = 0;
    const queue = {
      ...printingQueue,
      remaining_seconds: 67 * 60,
      entries: printingQueue.entries.map((entry) =>
        entry.state === 'waiting' && waiting++ === 0
          ? { ...entry, estimated_seconds: 2 * 3600 }
          : entry,
      ),
    };
    const screen = await render(PrinterQueue, {
      view: queueView(queue, 'ready'),
      t,
      me: ME,
      now,
    });

    await expect.element(page.getByTestId('queue-timeline')).toBeVisible();
    const bars = screen.container.querySelectorAll(
      '[data-part="timeline-slot"]',
    );
    expect(bars).toHaveLength(queue.entries.length);
    expect(bars[0]?.getAttribute('data-known')).toBe('true');
    expect(bars[2]?.getAttribute('data-known')).toBe('false');
    await expect
      .element(page.getByTestId('timeline-unknown'))
      .toBeInTheDocument();
    // The scale is fixed: two hours are two thirds of a 200 pixel mark of
    // three hours, less the gap between two bars, whatever else the queue
    // holds. The axis is three days wide, so it scrolls inside the card.
    expect((bars[1] as HTMLElement).style.width).toBe('131.333px');
    // A bar names on hover when its file prints and for how long.
    expect(bars[1]?.getAttribute('title')).toMatch(/– .* · 2 hr/u);
    expect(bars[2]?.getAttribute('title')).toMatch(/ · length unknown$/u);
    const scroll = screen.container.querySelector<HTMLElement>(
      '[data-part="timeline-scroll"]',
    );
    expect(scroll?.scrollWidth).toBeGreaterThanOrEqual(2400);
    // A mark names its hour, and the one at midnight the day it starts.
    expect(scroll?.textContent).toContain('12:00');
    expect(scroll?.textContent).toContain('Sat, Nov 14');
    expect(scroll?.textContent).not.toContain('00:00');

    // The file on the printer is the first row of the one list, with when it
    // ends. The files behind it say when they print.
    const list = screen.container.querySelector('[data-part="waiting-list"]');
    expect(list?.querySelector('[data-part="active-entry"]')).not.toBeNull();
    const times = Array.from(
      screen.container.querySelectorAll('[data-part="entry-times"]'),
    ).map((element) => element.textContent.replace(/\s+/gu, ' ').trim());
    expect(times[0]).toContain('ends at about');
    expect(times[0]).toContain('10:42');
    expect(times[1]).toContain('10:45');
    expect(times[1]).toContain('12:45');
    // Behind two known lengths the third start is firm, and its end is not.
    expect(times[2]).toContain('from');
    expect(times[2]).toContain('12:48');
    expect(times[2]).toContain('length unknown');
  });

  it('draws no bar for a queue with nothing in it', async () => {
    const screen = await render(PrinterQueue, {
      view: queueView({ ...waitingQueue, entries: [] }, 'ready'),
      t,
      me: ME,
    });

    await expect.element(page.getByTestId('queue-status')).toBeVisible();
    expect(
      screen.container.querySelector('[data-part="queue-timeline"]'),
    ).toBeNull();
  });

  it('moves a file and takes one off the queue', async () => {
    const move = vi.fn(() => Promise.resolve());
    const remove = vi.fn<(entry: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    await render(PrinterQueue, {
      view: queueView(waitingQueue, 'ready', { move, remove }),
      t,
      me: ME,
    });

    await expect
      .element(page.getByRole('button', { name: 'Move bracket.bgcode up' }))
      .toBeDisabled();
    await page
      .getByRole('button', { name: 'Move bracket.bgcode down' })
      .click();
    expect(move).toHaveBeenCalledWith('e1', 1);

    await page
      .getByRole('button', { name: 'Move lid-0.4n-PETG.bgcode up' })
      .click();
    expect(move).toHaveBeenCalledWith('e3', 1);

    await page
      .getByRole('button', { name: 'Remove hinge-left.bgcode from the queue' })
      .click();
    expect(remove).toHaveBeenCalledWith('e2');
  });

  it('starts the next file with one button', async () => {
    const printNext = vi.fn(() => Promise.resolve());
    await render(PrinterQueue, {
      view: queueView(waitingQueue, 'ready', { printNext }),
      t,
      me: ME,
    });

    await expect
      .poll(() => page.getByTestId('outlook').element().textContent)
      .toContain('bracket.bgcode');
    await page.getByRole('button', { name: 'Set ready and print' }).click();
    expect(printNext).toHaveBeenCalledOnce();
    // With a file waiting the one button says both, so there is no second.
    await expect
      .element(page.getByRole('button', { name: 'Set ready', exact: true }))
      .not.toBeInTheDocument();
    // The printer is not ready yet, so there is nothing to unset.
    await expect
      .element(page.getByRole('button', { name: 'Unset ready' }))
      .toBeDisabled();
  });

  it('only sets the printer ready when nothing waits', async () => {
    const setReady = vi.fn(() => Promise.resolve());
    await render(PrinterQueue, {
      view: queueView({ ...waitingQueue, entries: [] }, 'ready', { setReady }),
      t,
      me: ME,
    });

    await page.getByRole('button', { name: 'Set ready', exact: true }).click();
    expect(setReady).toHaveBeenCalledWith(true);
  });

  it('says nothing above a file that has its own card below', async () => {
    const screen = await render(PrinterQueue, {
      view: queueView(printingQueue, 'ready'),
      t,
      me: ME,
    });

    await expect.element(page.getByRole('switch')).toBeInTheDocument();
    const status = screen.container.querySelector('[data-part="queue-status"]');
    expect(status?.querySelector('[data-part="outlook"]')).toBeNull();
    expect(
      screen.container.querySelector('[data-part="active-entry"]'),
    ).not.toBeNull();
  });

  it('draws no status card over a printing file on a printer with no ready state', async () => {
    const screen = await render(PrinterQueue, {
      view: queueView(
        {
          ...printingQueue,
          printer: { ...printingQueue.printer, can_be_ready: false },
        },
        'ready',
      ),
      t,
      me: ME,
    });

    await expect
      .poll(() => screen.container.querySelector('[data-part="active-entry"]'))
      .not.toBeNull();
    expect(
      screen.container.querySelector('[data-part="queue-status"]'),
    ).toBeNull();
  });

  it('greys the start out on a printer that is ready already', async () => {
    await render(PrinterQueue, {
      view: queueView(readyQueue, 'ready'),
      t,
      me: ME,
    });

    await expect
      .element(page.getByRole('button', { name: 'Print next' }))
      .toBeDisabled();
    await expect
      .element(page.getByRole('button', { name: 'Unset ready' }))
      .toBeEnabled();
  });

  // A ready printer with a file waiting and the switch off starts nothing by
  // itself. That is said in a box, the switch is marked, and Print works.
  it('says in a box that the switch is off on a ready printer', async () => {
    const printNext = vi.fn(() => Promise.resolve());
    await render(PrinterQueue, {
      view: queueView({ ...readyQueue, starts_alone: false }, 'ready', {
        printNext,
      }),
      t,
      me: ME,
    });

    await expect
      .poll(() => page.getByTestId('alone-off').element().textContent)
      .toContain('turn on the switch below');
    expect(
      page.getByTestId('alone').element().classList.contains('border-warning'),
    ).toBe(true);

    await page.getByRole('button', { name: 'Print next' }).click();
    expect(printNext).toHaveBeenCalledOnce();
  });

  it('leaves the start to a person who may not turn the switch on', async () => {
    await render(PrinterQueue, {
      view: {
        ...queueView({ ...readyQueue, starts_alone: false }, 'ready'),
        mayAllow: false,
      },
      t,
      me: ME,
    });

    await expect
      .element(page.getByRole('button', { name: 'Print next' }))
      .toBeEnabled();
    await expect.element(page.getByRole('switch')).toBeDisabled();
    await expect
      .poll(() => page.getByTestId('outlook').element().textContent)
      .toContain('Start the file here');
  });

  it('leaves the start to a person when a note holds the file on a ready printer', async () => {
    await render(PrinterQueue, {
      view: queueView(
        {
          ...readyQueue,
          entries: readyQueue.entries.map((entry, index) =>
            index === 0 ? { ...entry, note: 'no_storage' as const } : entry,
          ),
        },
        'ready',
      ),
      t,
      me: ME,
    });

    await expect
      .element(page.getByRole('button', { name: 'Print next' }))
      .toBeEnabled();
    await expect
      .poll(() => page.getByTestId('outlook').element().textContent)
      .toContain('did not start by itself');
  });

  it('switches whether the queue prints when the printer is marked ready', async () => {
    const setAlone = vi.fn(() => Promise.resolve());
    await render(PrinterQueue, {
      view: queueView(manualQueue, 'ready', { setAlone }),
      t,
      me: ME,
    });

    await page
      .getByRole('switch', {
        name: 'Let the queue start the next file by itself',
      })
      .click();
    expect(setAlone).toHaveBeenCalledWith(true);
  });

  it('says a file is on its way, and offers nothing to take it back', async () => {
    await render(PrinterQueue, {
      view: queueView(sendingQueue, 'ready'),
      t,
      me: ME,
    });

    await expect
      .poll(() => page.getByTestId('active-entry').element().textContent)
      .toContain('Started by you');
    // A send that never ends is given back by the queue after its timeout,
    // and a transfer is stopped where every transfer is.
    await expect
      .element(page.getByRole('button', { name: 'Back to queue' }))
      .not.toBeInTheDocument();
  });

  it('says why a file did not start', async () => {
    await render(PrinterQueue, { view: queueView(heldQueue), t, me: ME });

    const notes = page.getByTestId('entry-note');

    await expect
      .poll(() => notes.first().element().textContent)
      .toContain('The printer did not take the file');
    await expect
      .poll(() => notes.first().element().textContent)
      .toContain('storage is full');
    await expect
      .poll(() => page.getByTestId('outlook').element().textContent)
      .toContain('did not start. Read its note');
  });

  it('shows a reader who may not change the queue no button that changes it', async () => {
    await render(PrinterQueue, { view: queueView(readOnlyQueue), t, me: ME });

    await expect.element(page.getByTestId('read-only')).toBeVisible();
    // The one button left opens the list of ended prints, which only reads,
    // besides the zoom of the timeline, which changes only the view.
    const buttons = page
      .getByRole('button')
      .elements()
      .filter((button) => !button.closest('[data-part="timeline-zoom"]'))
      .map((button) => button.textContent.trim());
    expect(buttons).toHaveLength(1);
    expect(buttons[0]).toMatch(/^Ended/u);

    await page.getByRole('button', { name: /^Ended/u }).click();
    await expect
      .element(page.getByTestId('history-entry').first())
      .toBeVisible();
    await expect
      .element(page.getByTestId('clear-history'))
      .not.toBeInTheDocument();
  });

  it('draws an empty queue', async () => {
    await render(PrinterQueue, { view: queueView(emptyQueue), t, me: ME });

    await expect
      .element(page.getByText('Nothing waits', { exact: true }))
      .toBeVisible();
  });

  it('draws a refusal in its own words', async () => {
    await render(PrinterQueue, {
      view: queueView(undefined, 'refused'),
      t,
      me: ME,
    });

    await expect
      .element(page.getByText('You may not see this queue'))
      .toBeVisible();
  });

  it('keeps an action that failed above the queue', async () => {
    await render(PrinterQueue, {
      view: queueView(waitingQueue, 'ready', { actionFault: refusal }),
      t,
      me: ME,
    });

    await expect
      .element(page.getByText('You may not print on this printer'))
      .toBeVisible();
  });
});

describe('the print from the queue on the Queue tab', () => {
  // A person who may print on the printer sees how far the print is, and
  // pauses, resumes or stops it without leaving the queue.
  it('shows how far the print is, and pauses it', async () => {
    const pause = vi.fn(() => Promise.resolve());
    await render(PrinterQueue, {
      view: queueView({ ...printingQueue, progress: 37 }, 'ready', { pause }),
      t,
      me: ME,
    });

    await expect.element(page.getByText('37 %')).toBeVisible();
    await page.getByRole('button', { name: 'Pause' }).click();
    expect(pause).toHaveBeenCalledOnce();
  });

  it('resumes a paused print', async () => {
    const resume = vi.fn(() => Promise.resolve());
    await render(PrinterQueue, {
      view: queueView(
        {
          ...printingQueue,
          printer: { ...printingQueue.printer, state: 'PAUSED', paused: true },
        },
        'ready',
        { resume },
      ),
      t,
      me: ME,
    });

    // The file reads as paused, as the printer's own badge does, not as
    // printing.
    await expect
      .element(page.getByTestId('active-entry').getByText('Paused'))
      .toBeVisible();
    expect(
      page
        .getByTestId('active-entry')
        .getByText('Printing', { exact: true })
        .elements(),
    ).toHaveLength(0);

    await page.getByRole('button', { name: 'Resume' }).click();
    expect(resume).toHaveBeenCalledOnce();
  });

  // A stopped print does not go on, so Stop asks first.
  it('stops the print only once the person confirms', async () => {
    const stop = vi.fn(() => Promise.resolve());
    await render(PrinterQueue, {
      view: queueView(printingQueue, 'ready', { stop }),
      t,
      me: ME,
    });

    await page.getByRole('button', { name: 'Stop', exact: true }).click();
    await page.getByRole('button', { name: 'Keep printing' }).click();
    expect(stop).not.toHaveBeenCalled();

    await page.getByRole('button', { name: 'Stop', exact: true }).click();
    await page.getByRole('button', { name: 'Stop the print' }).click();
    expect(stop).toHaveBeenCalledOnce();
  });

  it('offers no pause or stop to a person who may not print there', async () => {
    await render(PrinterQueue, {
      view: queueView({ ...printingQueue, may_change: false }),
      t,
      me: ME,
    });

    await expect
      .element(page.getByTestId('print-progress'))
      .toBeInTheDocument();
    expect(page.getByRole('button', { name: 'Pause' }).elements()).toHaveLength(
      0,
    );
    expect(
      page.getByRole('button', { name: 'Stop', exact: true }).elements(),
    ).toHaveLength(0);
  });
});

describe('adding files on the Queue tab', () => {
  it('ticks files on the printer and in a storage, and adds them in that order', async () => {
    const addFiles = vi.fn((files: readonly FileAt[]) =>
      Promise.resolve({ added: files.length, failed: [] }),
    );
    await render(PrinterQueue, {
      view: queueView(emptyQueue, 'ready', { addFiles }),
      t,
      me: ME,
      picker: () => new FilePicker(fakeFilesApi(), 'mk4-1'),
    });

    await page.getByRole('button', { name: 'Add files' }).click();
    await expect.element(page.getByText('benchy.bgcode')).toBeInTheDocument();
    await expect
      .element(page.getByText('prusa_printer_settings.ini'))
      .not.toBeInTheDocument();
    await page.getByRole('checkbox', { name: /benchy\.bgcode/u }).click();
    await page.getByRole('radio', { name: 'Storage' }).click();
    await page.getByRole('checkbox', { name: /lid-0\.4n-PETG/u }).click();
    await expect
      .poll(() => page.getByRole('dialog').element().textContent)
      .toContain('2 files selected');
    await page.getByRole('button', { name: 'Add 2 files' }).click();

    expect(addFiles).toHaveBeenCalledWith([
      { path: 'usb/benchy.bgcode' },
      { storage: 'local', path: 'lid-0.4n-PETG.bgcode' },
    ]);
    await expect.element(page.getByRole('dialog')).not.toBeInTheDocument();
  });

  it('searches every file by name and switches to the folders', async () => {
    await render(PrinterQueue, {
      view: queueView(emptyQueue, 'ready'),
      t,
      me: ME,
      picker: () => new FilePicker(fakeFilesApi(), 'mk4-1'),
    });

    await page.getByRole('button', { name: 'Add files' }).click();
    const dialog = page.getByRole('dialog');
    // A file in a folder is listed with the folder it is in.
    await expect.element(dialog.getByText('gear.bgcode')).toBeInTheDocument();
    await expect.element(dialog.getByText(/^models ·/u)).toBeInTheDocument();

    await dialog
      .getByRole('searchbox', { name: 'Search by name' })
      .fill('GEAR');
    await expect
      .element(dialog.getByText('benchy.bgcode'))
      .not.toBeInTheDocument();
    await expect.element(dialog.getByText('gear.bgcode')).toBeInTheDocument();

    // The box's own clear button empties it, in every browser.
    await dialog.getByRole('button', { name: 'Clear search' }).click();
    await expect.element(dialog.getByText('benchy.bgcode')).toBeInTheDocument();
    await dialog
      .getByRole('searchbox', { name: 'Search by name' })
      .fill('GEAR');

    await dialog.getByRole('radio', { name: 'Folders' }).click();
    await expect
      .element(dialog.getByRole('button', { name: 'models' }))
      .toBeInTheDocument();
    await expect.element(dialog.getByText('benchy.bgcode')).toBeInTheDocument();
    await expect
      .element(dialog.getByText('gear.bgcode'))
      .not.toBeInTheDocument();
  });

  it('copies a stored file to a printer that is there, and queues it later on one away', async () => {
    for (const connected of [true, false]) {
      const addFiles = vi.fn((files: readonly FileAt[]) =>
        Promise.resolve({ added: files.length, failed: [] }),
      );
      const send = vi.fn();
      const queue = {
        ...emptyQueue,
        printer: { ...emptyQueue.printer, connected },
      };
      const screen = await render(PrinterQueue, {
        view: queueView(queue, 'ready', { addFiles }),
        t,
        me: ME,
        picker: () => new FilePicker(fakeFilesApi(), 'mk4-1'),
        send,
      });

      await page.getByRole('button', { name: 'Add files' }).click();
      await page.getByRole('radio', { name: 'Storage' }).click();
      // The dialog says so where a stored file waits to be copied.
      if (connected) {
        await expect
          .element(page.getByTestId('stored-later'))
          .not.toBeInTheDocument();
      } else {
        await expect
          .element(page.getByTestId('stored-later'))
          .toBeInTheDocument();
      }
      await page.getByRole('checkbox', { name: /lid-0\.4n-PETG/u }).click();
      await page.getByRole('button', { name: 'Add 1 file' }).click();

      if (connected) {
        expect(send).toHaveBeenCalledWith([
          { storage: 'local', path: 'lid-0.4n-PETG.bgcode' },
        ]);
        expect(addFiles).not.toHaveBeenCalled();
      } else {
        expect(send).not.toHaveBeenCalled();
        expect(addFiles).toHaveBeenCalledWith([
          { storage: 'local', path: 'lid-0.4n-PETG.bgcode' },
        ]);
      }
      await screen.unmount();
    }
  });

  it('keeps the files the queue refused above it', async () => {
    const addFiles = vi.fn(() =>
      Promise.resolve({
        added: 0,
        failed: [
          {
            name: 'benchy.bgcode',
            fault: {
              code: undefined,
              title: 'The queue is full',
              detail: undefined,
            },
          },
        ],
      }),
    );
    await render(PrinterQueue, {
      view: queueView(waitingQueue, 'ready', { addFiles }),
      t,
      me: ME,
      picker: () => new FilePicker(fakeFilesApi(), 'mk4-1'),
    });

    await page.getByRole('button', { name: 'Add files' }).click();
    await page.getByRole('checkbox', { name: /benchy\.bgcode/u }).click();
    await page.getByRole('button', { name: 'Add 1 file' }).click();

    await expect
      .poll(() => document.body.textContent)
      .toContain('1 file was not added');
    await expect
      .poll(() => document.body.textContent)
      .toContain('benchy.bgcode: The queue is full');
  });

  it('offers no Add files to a reader who may not change the queue', async () => {
    await render(PrinterQueue, {
      view: queueView(readOnlyQueue),
      t,
      me: ME,
      picker: () => new FilePicker(fakeFilesApi(), 'mk4-1'),
    });

    await expect.element(page.getByTestId('waiting-list')).toBeInTheDocument();
    expect(
      page.getByRole('button', { name: 'Add files' }).elements(),
    ).toHaveLength(0);
  });

  it('cuts a long file name short instead of widening the dialog', async () => {
    const files = fakeFilesApi();
    const long = `usb/${'Flower Pot_0.4n_0.2mm_PLA_XLIS_'.repeat(6)}1h52m.bgcode`;
    await render(PrinterQueue, {
      view: queueView(emptyQueue, 'ready'),
      t,
      me: ME,
      picker: () =>
        new FilePicker(
          {
            ...files,
            listDeviceFiles: (request, signal) =>
              files.listDeviceFiles(request, signal).then((listed) =>
                request.query?.path === 'usb'
                  ? {
                      ...listed,
                      items: [
                        ...(listed.items ?? []),
                        {
                          ...(listed.items ?? [])[1]!,
                          path: long,
                          name: long.slice(4),
                          display_name: long.slice(4),
                        },
                      ],
                    }
                  : listed,
              ),
          },
          'mk4-1',
        ),
    });

    await page.getByRole('button', { name: 'Add files' }).click();
    await expect
      .element(page.getByRole('dialog').getByText(/1h52m\.bgcode$/u))
      .toBeInTheDocument();
    // The name stays inside its row, where it is cut short, and does not
    // push the row and the dialog wider.
    const name = page
      .getByRole('dialog')
      .getByText(/1h52m\.bgcode$/u)
      .element() as HTMLElement;
    expect(name.offsetWidth).toBeLessThanOrEqual(
      name.parentElement?.clientWidth ?? 0,
    );
  });

  it('takes several ticked files off the queue at once', async () => {
    const remove = vi.fn<(entry: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    await render(PrinterQueue, {
      view: queueView(waitingQueue, 'ready', { remove }),
      t,
      me: ME,
    });

    const ids = page
      .getByTestId('waiting-entry')
      .elements()
      .map((row) => row.getAttribute('data-entry'));
    // Ticking moves nothing: the actions take the place of adding files.
    const list = page.getByTestId('waiting-list');
    const before = list.element().getBoundingClientRect().height;
    const boxes = page.getByTestId('waiting-entry').getByRole('checkbox');
    await boxes.nth(0).click();
    await boxes.nth(1).click();
    expect(list.element().getBoundingClientRect().height).toBe(before);
    await page.getByRole('button', { name: 'Remove 2 files' }).click();

    await expect.poll(() => remove.mock.calls.length).toBe(2);
    expect(remove.mock.calls.map(([id]) => id)).toEqual(ids.slice(0, 2));
  });

  it('names where a file on the printer lies', async () => {
    await render(PrinterQueue, { view: queueView(onPrinterQueue), t, me: ME });

    await expect
      .poll(() => page.getByTestId('waiting-list').element().textContent)
      .toContain('usb/models');
  });
});
