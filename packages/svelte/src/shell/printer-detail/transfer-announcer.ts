// What says a file reached the printer.

import { type DataFault, type PrintEnd, TRANSFER_RUNNING } from '../../data';
import { describeFault, t } from '../../i18n';
import { toast } from '../../primitives';
import type { PrinterTransferRow } from './status';

// How long one failure of a file counts as already said.
const SAID_ONCE_MS = 5000;

// A transfer a person stopped answers its send with a fault too, and is not a
// failure to tell them about.
const stoppedByPerson = (fault: DataFault): boolean =>
  fault.params.event === 'stopped';

/**
 * Raises one toast per transfer that ends while the page is open.
 *
 * A landing happens away from the eye. By then the transfer's own row is gone
 * and the folder listing looks no different, so without this nothing on the
 * screen says the file arrived.
 *
 * The store is shared between pages, so its rows may come from an earlier
 * visit. Nothing is recorded until the page's own read of the listing has
 * answered, which the caller says with `read`. The rows of that read are
 * recorded without a toast. After it, a row is announced when it was seen
 * running and then ended, or when it first appears already ended. Switching
 * printers starts a fresh record.
 */
export class TransferAnnouncer {
  #printer = '';
  #seen = new Map<string, string>();
  #listed = false;
  // When each file was last said to have failed. A send that fails can be
  // said by its row and by the caller that holds its answer.
  #failed = new Map<string, number>();

  observe(
    printer: string,
    rows: readonly PrinterTransferRow[],
    read: boolean,
  ): void {
    if (this.#printer !== printer) {
      this.#printer = printer;
      this.#seen = new Map();
      this.#listed = false;
    }
    if (!read) return;

    for (const row of rows) {
      const before = this.#seen.get(row.transferId);
      this.#seen.set(row.transferId, row.state);
      if (!this.#listed || TRANSFER_RUNNING.includes(row.state)) continue;

      const wasRunning =
        before !== undefined && TRANSFER_RUNNING.includes(before);
      if (before !== undefined && !wasRunning) continue;

      const name = row.path.split('/').pop() ?? row.path;
      if (row.state === 'done') {
        toast.success(t('printerDetailFiles.transfers.landed', { name }));
      } else if (row.state === 'failed') {
        // A transfer a person stopped, and one the machine cut off, both
        // already say so in the row they keep.
        // The row carries no fault code, so a refusal and a transfer that
        // broke on the way share one sentence with the server's reason.
        const reason = row.reason ?? '';
        this.#fail(
          name,
          reason === ''
            ? t('printerDetailFiles.transfers.lost', { name })
            : t('printerDetailFiles.transfers.lostBecause', { name, reason }),
        );
      }
    }

    this.#listed = true;
  }

  /**
   * Says a file was not sent, with the reason the fault reads as, for a
   * send the server refused before any transfer began, such as a printer
   * still fetching another file or a replace the printer refused to make
   * room for. No row exists for such a send, so nothing else says it.
   */
  notSent(name: string, fault: DataFault): void {
    if (stoppedByPerson(fault)) return;

    const text = describeFault(fault);
    const reason = [text.title, text.detail].filter(Boolean).join(': ');
    this.#fail(
      name,
      t('printerDetailFiles.transfers.lostBecause', { name, reason }),
    );
  }

  // One failure of a file is said once, whichever of its row and its answer
  // comes first.
  #fail(name: string, text: string): void {
    const now = Date.now();
    const said = this.#failed.get(name);
    this.#failed.set(name, now);
    if (said !== undefined && now - said < SAID_ONCE_MS) return;

    toast.error(text);
  }

  /**
   * Says a file was not printed, with the reason the fault reads as. A file
   * the printer took and would not start is said to be on the printer, after
   * its landing; one whose request failed before the printer answered is only
   * said not to have printed.
   */
  notPrinted(name: string, ended: PrintEnd): void {
    if (ended.fault === undefined || stoppedByPerson(ended.fault)) return;

    const text = describeFault(ended.fault);
    const reason = [text.title, text.detail].filter(Boolean).join(': ');
    this.#fail(
      name,
      ended.answer === undefined
        ? t('printerDetailFiles.transfers.notPrinted', { name, reason })
        : t('printerDetailFiles.transfers.notStartedBecause', { name, reason }),
    );
  }
}
