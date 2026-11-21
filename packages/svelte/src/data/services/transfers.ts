// The requests and the events behind one printer's file transfers. A service
// names the SDK operation and its parameters and keeps no state
// (svelte-data.md).
import type { PrusaConnect, EventType, StreamEvent } from '@prusa/connect';

import { isTemporarySource } from './storage';

/** The transfers of one printer, as `list-device-transfers` answers. */
export type TransferPage = Awaited<
  ReturnType<PrusaConnect['listDeviceTransfers']>
>;

/** One transfer as the listing reports it. */
export type Transfer = NonNullable<TransferPage['items']>[number];

/** What `send-device-file` answers: the path on the printer and the transfer. */
export type SentFile = Awaited<ReturnType<PrusaConnect['sendDeviceFile']>>;

/** Every transfer still running on a printer the caller may read. */
export type RunningTransferPage = Awaited<
  ReturnType<PrusaConnect['listRunningTransfers']>
>;

/** One running transfer as `list-running-transfers` answers it. */
export type RunningTransfer = NonNullable<RunningTransferPage['items']>[number];

/** What the store of every running transfer needs of the SDK. */
export type RunningTransfersClient = Pick<
  PrusaConnect,
  'listRunningTransfers' | 'stopDeviceTransfer'
>;

/** What the transfers store needs of the SDK, and no more. */
export type TransfersClient = Pick<
  PrusaConnect,
  | 'listDeviceTransfers'
  | 'stopDeviceTransfer'
  | 'sendDeviceFile'
  | 'printDeviceFile'
  | 'events'
>;

/**
 * The three transitions of a transfer. Started and settled are durable and
 * progressed is telemetry: it is superseded by the next one and never stored,
 * so a reader that joined late gets the proportion from the frame after it.
 */
export const TRANSFER_EVENTS: readonly EventType[] = [
  'com.prusa.connect.device.transfer.started',
  'com.prusa.connect.device.transfer.progressed',
  'com.prusa.connect.device.transfer.settled',
];

/**
 * One transfer as the store keeps it, folded from the listing and from the
 * three events.
 *
 * It is the store's own shape rather than the listing's row. A transfer the
 * store first learns about from a started frame has no `started_at` and no
 * `state`, so a row built from that frame could not satisfy the listing type.
 */
export interface TransferRow {
  readonly transferId: string;
  /** The storage the bytes come from, on this server. */
  readonly storage: string;
  /** The path inside that storage. */
  readonly source: string;
  /** Where the file lands on the printer, storage-prefixed. */
  readonly path: string;
  readonly bytesTotal: number;
  readonly bytesSent: number;
  /** `queued`, `sending`, `done`, `failed`, `cancelled` or `interrupted`. */
  readonly state: string;
  /** Why it ended that way, in the words of whoever ended it. */
  readonly reason: string | undefined;
  /** When it began, RFC 3339. Absent on a row known only from the event stream. */
  readonly startedAt?: string | undefined;
  /** When it ended, RFC 3339. Absent while it runs and on a row known only from the event stream. */
  readonly endedAt?: string | undefined;
}

/** The two states a transfer that is still running reports. */
export const TRANSFER_RUNNING: readonly string[] = ['queued', 'sending'];

/** The state of a transfer whose file reached the printer whole. */
export const TRANSFER_DONE = 'done';

/**
 * Where each file this server sent lands on the printer, against the picture
 * of the plate it was sliced from.
 *
 * The transfer is the only record that names both ends. A printer's own file
 * listing carries no digest, and a printer never hands its bytes back. A match
 * on the file name could draw another model's plate without saying so. A file
 * carried over on a USB stick has no transfer, and so no picture.
 *
 * The rows are newest first. The first row naming a path is the transfer that
 * wrote what the printer holds there now. Only a transfer that finished gives
 * a picture. Where the newest one did not finish, the path gets no picture,
 * because the file there may be the old one or a part of the new one. Nor
 * does one sent from a temporary copy, which the server no longer holds.
 */
export const thumbnailByLanding = (
  rows: readonly TransferRow[],
  urlOf: (storage: string, source: string) => string,
): ReadonlyMap<string, string> => {
  const decided = new Set<string>();
  const pictures = new Map<string, string>();

  for (const row of rows) {
    if (decided.has(row.path)) continue;
    decided.add(row.path);
    // A temporary copy is deleted once it lands, so the picture is the one
    // the printer reads out of its own file.
    if (row.state === TRANSFER_DONE && !isTemporarySource(row.source)) {
      pictures.set(row.path, urlOf(row.storage, row.source));
    }
  }

  return pictures;
};

/**
 * The transfer that put the file a print ran on the printer: the newest one
 * to that path that had finished when the print started. A send still running
 * then had not replaced the file, and a send after the print is another file.
 *
 * The rows are compared by their times, never by their order. Two sends that
 * finished at the same moment are told apart by the later start. A row with no
 * finish time cannot be placed before the print and is left out.
 */
export const lastSentBefore = (
  rows: readonly TransferRow[],
  path: string,
  printStarted: string,
): TransferRow | undefined => {
  const printAt = Date.parse(printStarted);
  const timeOf = (value: string | undefined): number =>
    value === undefined ? Number.NaN : Date.parse(value);

  let found: TransferRow | undefined;
  for (const row of rows) {
    if (row.path !== path || row.state !== 'done') continue;
    const ended = timeOf(row.endedAt);
    if (Number.isNaN(ended) || ended > printAt) continue;

    const best = timeOf(found?.endedAt);
    const newer =
      found === undefined ||
      ended > best ||
      (ended === best &&
        (timeOf(row.startedAt) || 0) > (timeOf(found.startedAt) || 0));
    if (newer) found = row;
  }

  return found;
};

const textOf = (value: unknown): string | undefined =>
  typeof value === 'string' && value !== '' ? value : undefined;

const countOf = (value: unknown): number | undefined =>
  typeof value === 'number' && Number.isFinite(value) ? value : undefined;

/** Reads one listing row into the store's shape. */
export const rowOf = (transfer: Transfer): TransferRow => ({
  transferId: transfer.transfer_id,
  storage: transfer.storage,
  source: transfer.source,
  path: transfer.path,
  bytesTotal: transfer.bytes_total,
  bytesSent: transfer.bytes_sent,
  state: transfer.state,
  reason: textOf(transfer.reason),
  startedAt: transfer.started_at,
  endedAt: transfer.ended_at,
});

/**
 * What one transfer event says about one transfer. The core publishes no
 * payload schema, so every field is read behind a check rather than trusted.
 * The result is a patch: a member that is absent leaves the row's value alone.
 */
export interface TransferPatch {
  readonly transferId: string;
  readonly storage?: string;
  readonly source?: string;
  readonly path?: string;
  readonly bytesTotal?: number;
  readonly bytesSent?: number;
  readonly state?: string;
  readonly reason?: string;
}

const SETTLED = 'com.prusa.connect.device.transfer.settled';
const STARTED = 'com.prusa.connect.device.transfer.started';

/**
 * Reads one transfer event. It answers undefined for an event that names no
 * transfer, which is a frame from a build this one does not know.
 */
export const patchOf = (event: StreamEvent): TransferPatch | undefined => {
  const payload = event.payload;
  if (typeof payload !== 'object' || payload === null) return undefined;

  // The payload crosses into this code as `unknown`, and this narrows it to
  // the members being read (typescript.md).
  const named = payload as Record<string, unknown>;
  const transferId = textOf(named.transfer_id);
  if (transferId === undefined) return undefined;

  const patch: {
    -readonly [K in keyof TransferPatch]: TransferPatch[K];
  } = { transferId };

  const storage = textOf(named.storage);
  if (storage !== undefined) patch.storage = storage;

  const source = textOf(named.source);
  if (source !== undefined) patch.source = source;

  const destination = textOf(named.destination);
  if (destination !== undefined) patch.path = destination;

  const bytesTotal = countOf(named.bytes_total);
  if (bytesTotal !== undefined) patch.bytesTotal = bytesTotal;

  const bytesSent = countOf(named.bytes_sent);
  if (bytesSent !== undefined) patch.bytesSent = bytesSent;

  const reason = textOf(named.reason);
  if (reason !== undefined) patch.reason = reason;

  // A started frame opens the transfer and a settled one closes it with the
  // state it ended in. A progress frame moves the bytes and never the state.
  if (event.type === STARTED) patch.state = 'sending';
  if (event.type === SETTLED) {
    patch.state = textOf(named.state) ?? 'failed';
  }

  return patch;
};

/** Every transfer of one printer that this server still knows about. */
export const readTransfers = (
  client: TransfersClient,
  deviceId: string,
): Promise<TransferPage> =>
  client.listDeviceTransfers({ path: { device_id: deviceId } });

/**
 * Every transfer still running on a printer the caller may read, whoever
 * started it.
 */
export const readRunningTransfers = (
  client: RunningTransfersClient,
): Promise<RunningTransferPage> => client.listRunningTransfers();

/** Stops one transfer. The printer forgets the bytes it already took. */
export const stopTransfer = (
  client: Pick<PrusaConnect, 'stopDeviceTransfer'>,
  deviceId: string,
  transferId: string,
): Promise<void> =>
  client.stopDeviceTransfer({
    path: { device_id: deviceId, transfer_id: transferId },
  });

/**
 * Sends one file from a storage on this server to the printer. The answer
 * names the path it lands on, which is what a print command then takes.
 */
export const sendFile = (
  client: TransfersClient,
  deviceId: string,
  storage: string,
  source: string,
  path: string,
  replace = false,
  keepBoth = false,
): Promise<SentFile> =>
  client.sendDeviceFile({
    path: { device_id: deviceId },
    body: {
      storage,
      source,
      path,
      ...(replace ? { replace: true } : {}),
      ...(keepBoth ? { keep_both: true } : {}),
    },
  });
