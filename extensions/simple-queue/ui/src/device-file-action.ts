// Add to queue in the menu of a file on a printer's Files tab, and as a
// choice for a file dropped on a printer's page. A file on the printer stays
// there, and the queue prints it from there when its turn comes. A dropped
// file is added while it is copied to the printer, and printed from there.
import { toast } from '@prusa/connect-ui/primitives';
import { type ModuleHost, parsePrinterFileRef } from '@prusa/connect-ui/slots';

import { addTo, type FileAt } from './adding';
import { localized } from './faults';
import type { QueueApi } from './api';

type Host = Pick<ModuleHost, 't' | 'fleet'>;

// Adds the file to the printer's queue and says where it landed. A refusal
// throws, and the host shows its reason.
const addAndSay = async (
  host: Host,
  api: Pick<QueueApi, 'addEntry'>,
  deviceId: string,
  file: FileAt,
): Promise<void> => {
  const added = await addTo(api, deviceId, file);
  const name = file.path.split('/').at(-1) ?? file.path;
  const printer = host.fleet.device(deviceId)?.name ?? deviceId;
  switch (added.kind) {
    case 'failed':
      throw new Error(localized(added.fault, host.t).title);
    case 'held':
      toast.warning(
        host.t('simple-queue.fileAction.held', {
          name,
          printer,
          reason: host.t(`simple-queue.note.${added.note}`),
        }),
      );
      return;
    case 'started':
      toast.success(
        host.t('simple-queue.fileAction.started', { name, printer }),
      );
      return;
    case 'added':
      toast.success(
        host.t('simple-queue.fileAction.added', {
          name,
          printer,
          position: added.position,
        }),
      );
  }
};

/**
 * Adds the file a printer file reference names to that printer's queue, and
 * says where it landed. A refusal throws, and the menu shows its reason.
 */
export const addPrinterFile = async (
  host: Host,
  api: Pick<QueueApi, 'addEntry'>,
  ref: string,
): Promise<void> => {
  const file = parsePrinterFileRef(ref);
  if (file === undefined)
    throw new Error(host.t('simple-queue.fileAction.absent'));

  await addAndSay(host, api, file.deviceId, { path: file.path });
};

/**
 * Adds a file dropped on a printer's page to that printer's queue while it
 * is still being copied there, and says where it landed. The queue looks for
 * the file on the printer when its turn comes.
 */
export const addUpload = async (
  host: Host,
  api: Pick<QueueApi, 'addEntry'>,
  ref: string,
): Promise<void> => {
  const file = parsePrinterFileRef(ref);
  if (file === undefined)
    throw new Error(host.t('simple-queue.fileAction.absent'));

  await addAndSay(host, api, file.deviceId, {
    path: file.path,
    arriving: true,
  });
};
