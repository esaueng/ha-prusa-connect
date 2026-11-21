// The one request that gets a file printing. A service names the SDK
// operation and its parameters and holds no state (svelte-data.md).
import type { PrusaConnect } from '@prusa/connect';

import type {
  PrintAction,
  PrintOutcome,
  PrintRequest,
} from '../../domain/actions';
import {
  type DeviceStorage,
  type DeviceStorages,
  writableStorageOf,
} from '../../domain/printer-storage';
import type { DataFault } from '../fault';

/** What starting a print needs of the SDK, and no more. */
export type PrintClient = Pick<
  PrusaConnect,
  'listDeviceStorages' | 'printDeviceFile'
>;

/** The storages one printer reports, as `list-device-storages` answers. */
export type DeviceStoragePage = DeviceStorages;

export type { DeviceStorage };

/**
 * The codes this library answers with when a file cannot be put on a printer.
 * They are this library's own and not the core's: no request was made.
 *
 * The two are different facts and take different answers. A printer that
 * answered with no storage it can write to needs a drive put in it. A printer
 * that has not answered yet, which a machine that has only just connected has
 * not, needs a moment.
 */
export const NO_PRINTER_STORAGE = 'ui.print/NO_STORAGE';
export const PRINTER_STORAGES_UNKNOWN = 'ui.print/STORAGES_UNKNOWN';

/** One of the two codes above, so a caller can switch on it exhaustively. */
export type NoPrinterStorageCode =
  typeof NO_PRINTER_STORAGE | typeof PRINTER_STORAGES_UNKNOWN;

/** Thrown when a printer has nowhere to put the file, or has not said yet. */
export class NoPrinterStorage extends Error {
  override readonly name = 'NoPrinterStorage';
  readonly code: NoPrinterStorageCode;

  constructor(printer: string, reported: boolean) {
    super(
      reported
        ? `${printer} has no storage it can write to`
        : `${printer} has not reported its storages`,
    );
    this.code = reported ? NO_PRINTER_STORAGE : PRINTER_STORAGES_UNKNOWN;
  }
}

/** The core's answer to a file sent to a name the printer already holds. */
export const FILE_NAME_TAKEN = 'core.printer/FILE_NAME_TAKEN';

/** A printer that refused the start; the print route answers it with `ok` false. */
export const COMMAND_REFUSED = 'core.printer/COMMAND_REFUSED';

/** The printer's refusal of the start as a fault, or undefined for a start it took. */
export const refusalOf = (outcome: PrintOutcome): DataFault | undefined =>
  outcome.ok
    ? undefined
    : {
        code: outcome.faultCode ?? COMMAND_REFUSED,
        status: undefined,
        params: {},
        message: outcome.machineReason,
        traceId: undefined,
      };

/** What `print-device-file` answers: the file, the transfer and the command. */
export type PrintAnswer = Awaited<ReturnType<PrusaConnect['printDeviceFile']>>;

/**
 * The verb a print is sent as. The core sends this same key for a print and
 * for a command a caller spells out, so a screen that asks whether a printer
 * takes prints asks about this key.
 */
export const START_PRINT_VERB = 'core.command.start_print';

/**
 * Where a file lands on one printer, as the print route spells a path: the
 * storage's own first segment, then the file's name.
 *
 * The printer addresses every file that way, `usb/bracket.bgcode`, so a bare
 * name would address the tree above the storages and reach no storage at all.
 */
export const landingPathOf = (
  storages: readonly DeviceStorage[],
  fileName: string,
): string | undefined => {
  const writable = writableStorageOf(storages);

  return writable === undefined ? undefined : `${writable.name}/${fileName}`;
};

/**
 * The storages one printer reports, with what each of them allows.
 *
 * Without `refresh` the server answers what the printer last said and puts
 * nothing on the wire. With it the server asks the printer again, which is the
 * only way to learn of a USB drive put in or pulled out since.
 */
export const readDeviceStorages = (
  client: Pick<PrusaConnect, 'listDeviceStorages'>,
  deviceId: string,
  options: { refresh?: boolean; signal?: AbortSignal } = {},
): Promise<DeviceStoragePage> =>
  client.listDeviceStorages(
    {
      path: { device_id: deviceId },
      query: options.refresh === true ? { refresh: true } : {},
    },
    options.signal,
  );

/**
 * Sends one file from a storage of this server to a printer and starts it.
 *
 * It asks the printer for its storages first, because the print route takes
 * the path the file lands under on the machine and this server cannot guess
 * it. The printer is asked again rather than remembered, so a drive put in a
 * moment ago counts. A printer with nowhere to write is refused here, before
 * anything is sent.
 */
export const printStoredFile = async (
  client: PrintClient,
  request: {
    deviceId: string;
    storage: string;
    source: string;
    name: string;
    replace?: boolean | undefined;
  },
): Promise<PrintOutcome> => {
  const page = await readDeviceStorages(client, request.deviceId, {
    refresh: true,
  });
  const path = page.reported
    ? landingPathOf(page.items ?? [], request.name)
    : undefined;
  if (path === undefined) {
    throw new NoPrinterStorage(request.deviceId, page.reported);
  }

  return printFile(client, {
    deviceId: request.deviceId,
    path,
    source: request.source,
    storage: request.storage,
    replace: request.replace,
  });
};

/**
 * Starts one print and reads the answer.
 *
 * The route takes the file two ways. A file the machine already holds is named
 * by `path` alone. A file on one of this server's storages names `storage` and
 * `source` too, and then the server sends the bytes to the machine first and
 * `path` is where they land.
 *
 * A printer that refuses the command answers 200 with `command.ok` false, so a
 * caller reads `ok` and does not treat the answer as a success.
 */
export const printFile = async (
  client: Pick<PrusaConnect, 'printDeviceFile'>,
  request: PrintRequest,
): Promise<PrintOutcome> => {
  const answer = await client.printDeviceFile({
    path: { device_id: request.deviceId },
    body:
      request.storage === undefined
        ? { path: request.path }
        : {
            path: request.path,
            source: request.source ?? '',
            storage: request.storage,
            ...(request.replace === true ? { replace: true } : {}),
            ...(request.keepBoth === true ? { keep_both: true } : {}),
          },
  });

  return {
    commandId: answer.command.command_id,
    faultCode: answer.command.fault_code,
    machineReason: answer.command.machine_reason,
    ok: answer.command.ok,
    path: answer.path,
    transferId: answer.transfer?.transfer_id,
  };
};

/**
 * Starts one print from either place a file can be. A file on a storage of
 * this server is sent to the printer first. A file the printer already holds
 * is started by its path on the machine.
 */
export const startPrint = (
  client: PrintClient,
  request: PrintRequest,
): Promise<PrintOutcome> =>
  request.storage === undefined
    ? printFile(client, request)
    : printStoredFile(client, {
        deviceId: request.deviceId,
        name: request.path,
        source: request.source ?? '',
        storage: request.storage,
        replace: request.replace,
      });

/**
 * How many printers one send talks to at once. The print route answers only
 * after the file has reached the printer, so a send to a whole fleet holds one
 * request open per transfer, and the core runs at most 16 detached transfers
 * across the server.
 */
export const PRINT_FAN_OUT = 4;

/**
 * What one printer made of a file sent to several. `failure` is absent when the
 * print started; otherwise it is what `describeFault` reads: the thrown value,
 * or the code and words of a printer that refused.
 */
export interface PrintAttempt {
  readonly deviceId: string;
  readonly failure?: unknown;
}

/**
 * Sends one file to each printer the requests name, at most `limit` at a time,
 * and answers one attempt per request in the order they were given. One
 * printer failing never stops the others.
 */
export const printOnEach = async (
  print: PrintAction,
  requests: readonly PrintRequest[],
  limit = PRINT_FAN_OUT,
): Promise<PrintAttempt[]> => {
  const attempts: PrintAttempt[] = [];
  let next = 0;

  const attempt = async (request: PrintRequest): Promise<PrintAttempt> => {
    try {
      const failure = refusalOf(await print(request));

      return failure === undefined
        ? { deviceId: request.deviceId }
        : { deviceId: request.deviceId, failure };
    } catch (thrown) {
      return { deviceId: request.deviceId, failure: thrown };
    }
  };

  const lane = async (): Promise<void> => {
    for (let at = next++; at < requests.length; at = next++) {
      const request = requests[at];
      if (request !== undefined) attempts[at] = await attempt(request);
    }
  };

  await Promise.all(
    Array.from({ length: Math.min(limit, requests.length) }, lane),
  );

  return attempts;
};
