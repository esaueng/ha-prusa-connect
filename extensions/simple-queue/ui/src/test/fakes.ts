// What the stories and the tests draw instead of the server: the module's
// English texts, and answers in the shape the routes give.
import {
  type FaultText,
  locale,
  registerCatalogue,
  translateFor,
} from '@prusa/connect-ui/i18n';
import type { ModuleHost } from '@prusa/connect-ui/slots';

import type { Entry, Overview, Queue, QueueApi, Translate } from '../api';
import type { BoardView } from '../board-store.svelte';
import { FilePicker, type FilePickerView } from '../file-picker.svelte';
import type { FilesApi } from '../files';
import type { Grants } from '../grants';
import english from '../locales/en.json';
import { OverviewStore } from '../overview-store.svelte';
import type { Load, PrinterQueueView } from '../printer-store.svelte';
import { holdSession } from '../session';

// The module's English texts, read the way the host reads them, so a plural
// message is drawn as a person sees it.
export const NAMESPACE = 'com.github.aux.prusa3d.simple-queue';
registerCatalogue(NAMESPACE, 'en', english);
locale.set('en');

/** The module's English texts, with the params filled in. */
export const t: Translate = translateFor(NAMESPACE);

/** The person reading the page in every story. */
export const ME = 'user:alice';

const entry = (fields: Partial<Entry> & Pick<Entry, 'id' | 'path'>): Entry => {
  const slash = fields.path.lastIndexOf('/');

  return {
    storage: 'local',
    name: fields.path.slice(slash + 1),
    added_by: ME,
    added_at: '2026-10-29T08:10:00Z',
    state: 'waiting',
    changed_at: '2026-10-29T08:10:00Z',
    ...fields,
  };
};

const bracket = entry({ id: 'e1', path: 'parts/bracket.bgcode' });
const hinge = entry({
  id: 'e2',
  path: 'parts/hinge-left.bgcode',
  added_by: 'user:bob',
  added_at: '2026-10-29T08:20:00Z',
});
const lid = entry({
  id: 'e3',
  path: 'boxes/lid-0.4n-PETG.bgcode',
  added_at: '2026-10-29T08:40:00Z',
});

const benchy = entry({
  id: 'e0',
  path: 'tests/benchy.bgcode',
  state: 'printing',
  started_by: '',
  landing: 'usb/benchy.bgcode',
  attempts: 1,
  job_id: '41',
  changed_at: '2026-10-29T08:00:00Z',
});

const finished = entry({
  id: 'h1',
  path: 'parts/clip.bgcode',
  state: 'ended',
  outcome: 'finished',
  attempts: 1,
  ended_at: '2026-10-29T07:40:00Z',
  changed_at: '2026-10-29T07:40:00Z',
});
const failed = entry({
  id: 'h2',
  path: 'parts/clip-v2.bgcode',
  state: 'ended',
  outcome: 'failed',
  added_by: 'user:bob',
  attempts: 1,
  ended_at: '2026-10-29T06:10:00Z',
  changed_at: '2026-10-29T06:10:00Z',
});
const cancelled = entry({
  id: 'h3',
  path: 'tests/first-layer.bgcode',
  state: 'ended',
  outcome: 'cancelled',
  attempts: 1,
  ended_at: '2026-10-28T19:00:00Z',
  changed_at: '2026-10-28T19:00:00Z',
});

const base: Queue = {
  device_id: 'mk4-1',
  entries: [],
  printer: {
    name: 'MK4 by the window',
    model: 'MK4S',
    state: 'IDLE',
    connected: true,
    can_be_ready: true,
    free: true,
    ready: false,
  },
  starts_alone: true,
  may_change: true,
  limits: { max_waiting: 30, keep_history: 10 },
};

/** A printer whose queue holds nothing and never did. */
export const emptyQueue: Queue = base;

/** A print running and three files after it. */
export const printingQueue: Queue = {
  ...base,
  printer: { ...base.printer, state: 'PRINTING', free: false },
  entries: [benchy, bracket, hinge, lid],
};

/** The prints that ended on the printing queue's printer, newest first. */
export const printingHistory: readonly Entry[] = [finished, failed, cancelled];

/** Files waiting on an idle printer that starts them by itself. */
export const waitingQueue: Queue = {
  ...base,
  printer: { ...base.printer, state: 'FINISHED' },
  entries: [bracket, hinge, lid],
};

/** A file that lies on the printer's USB drive and waits there. */
export const onPrinterQueue: Queue = {
  ...waitingQueue,
  entries: [
    entry({ id: 'p1', storage: '', path: 'usb/models/gear-0.4n.bgcode' }),
    ...waitingQueue.entries,
  ],
};

/** The bed marked clear, so the next file starts in a moment. */
export const readyQueue: Queue = {
  ...waitingQueue,
  printer: { ...base.printer, state: 'READY', ready: true },
};

/** A print the queue started by itself. */
export const printingEntry: Entry = benchy;

/** A file sent by the reader, and the printer not yet printing it. */
export const sendingEntry: Entry = {
  ...bracket,
  state: 'sending',
  started_by: ME,
  landing: 'usb/bracket.bgcode',
  attempts: 1,
  transfer_id: '17',
  changed_at: '2026-10-29T08:50:00Z',
};

/** The file sent, and the printer not yet printing it. */
export const sendingQueue: Queue = { ...base, entries: [sendingEntry, hinge] };

/** A printer the module may not start prints on by itself. */
export const manualQueue: Queue = {
  ...waitingQueue,
  starts_alone: false,
};

/** The printer did not take the file, so the queue waits for a person. */
export const heldQueue: Queue = {
  ...waitingQueue,
  printer: { ...base.printer, state: 'IDLE' },
  entries: [
    {
      ...bracket,
      note: 'send_failed',
      detail: 'the printer refused the file: storage is full',
      attempts: 1,
    },
    { ...hinge, note: 'owner_may_not' },
    lid,
  ],
};

/** A queue seen by a person who may not print on the printer. */
export const readOnlyQueue: Queue = { ...printingQueue, may_change: false };

/** A printer that is away. */
export const awayQueue: Queue = {
  ...waitingQueue,
  printer: {
    ...base.printer,
    state: 'IDLE',
    connected: false,
    free: false,
  },
};

/** A file name that does not fit a phone. */
export const longQueue: Queue = {
  ...waitingQueue,
  entries: [
    entry({
      id: 'l1',
      path: 'customers/2026/october/a-very-long-folder-name/enclosure-front-panel-with-hinges-0.6n-0.32mm-PETG-MK4-final-v12.bgcode',
      added_by: 'user:a-person-with-a-very-long-account-name@example.org',
    }),
    ...waitingQueue.entries,
  ],
};

/** The overview of three printers. */
export const someQueues: Overview = {
  printers: [
    {
      device_id: 'mk4-1',
      printer: printingQueue.printer,
      waiting: 3,
      active: benchy,
      next: bracket,
    },
    {
      device_id: 'core-one',
      printer: {
        name: 'CORE One',
        model: 'COREONE',
        state: 'READY',
        connected: true,
        can_be_ready: true,
        free: true,
        ready: true,
      },
      waiting: 1,
      next: lid,
    },
    {
      device_id: 'mini',
      printer: {
        name: 'MINI in the office',
        state: 'IDLE',
        connected: false,
        can_be_ready: true,
        free: false,
        ready: false,
      },
      waiting: 0,
    },
  ],
};

export const noQueues: Overview = { printers: [] };

/** The queues of someQueues, whole, as the board route answers them. */
export const someBoard: Queue[] = [
  { ...printingQueue, device_id: 'mk4-1' },
  {
    ...readyQueue,
    device_id: 'core-one',
    printer: { ...readyQueue.printer, name: 'CORE One', model: 'COREONE' },
    entries: [lid],
  },
  {
    ...emptyQueue,
    device_id: 'mini',
    printer: {
      ...emptyQueue.printer,
      name: 'MINI in the office',
      model: 'MINI',
      connected: false,
    },
  },
];

const farmStates = ['PRINTING', 'FINISHED', 'IDLE', 'READY', 'PAUSED'] as const;

/**
 * Twenty printers, for a page that has to stay short: twelve with files and
 * eight with empty queues.
 */
export const manyBoard: Queue[] = Array.from({ length: 20 }, (_, index) => {
  const number = String(index + 1).padStart(2, '0');
  const state = farmStates[index % farmStates.length] ?? 'IDLE';
  const files = index < 12 ? 1 + (index % 4) : 0;
  const tagged = (one: Entry, at: number): Entry => ({
    ...one,
    id: `p${number}-e${String(at)}`,
  });
  const waiting = [bracket, hinge, lid, bracket]
    .slice(0, files)
    .map((one, at) => tagged(one, at));

  return {
    ...base,
    device_id: `printer-${number}`,
    printer: { ...base.printer, name: `Printer ${number}`, state },
    entries:
      state === 'PRINTING' && files > 0
        ? [tagged(benchy, 9), ...waiting]
        : waiting,
  };
});

/** A refusal as describeFault reads it. */
export const refusal: FaultText = {
  code: 'simple-queue/NOT_OPERATOR',
  title: 'You may not print on this printer',
  detail: 'you may not print on mk4-1',
};

export const failure: FaultText = {
  code: 'simple-queue/STORE_FAILED',
  title: 'The module could not save the queue',
  detail: 'Try again in a moment.',
};

// The ended prints of the fake queues that have any, as the history route
// answers them. A queue made from one of them by spreading it has them too.
const endedOf = (queue: Queue | undefined): readonly Entry[] => {
  if (queue === undefined) return [];
  if (queue.entries.some((entry) => entry.id === benchy.id)) {
    return printingHistory;
  }

  return queue.entries === waitingQueue.entries ? [finished] : [];
};

/** A view of one printer's queue that does nothing. */
export const queueView = (
  queue: Queue | undefined,
  load: Load = 'ready',
  fields: Partial<PrinterQueueView> = {},
): PrinterQueueView => ({
  load,
  fault: {
    loading: undefined,
    ready: undefined,
    refused: refusal,
    failed: failure,
  }[load],
  queue,
  history: endedOf(queue),
  actionFault: undefined,
  busy: false,
  printNext: () => Promise.resolve(),
  setReady: () => Promise.resolve(),
  remove: () => Promise.resolve(),
  move: () => Promise.resolve(),
  clearHistory: () => Promise.resolve(),
  setAlone: () => Promise.resolve(),
  pause: () => Promise.resolve(),
  resume: () => Promise.resolve(),
  stop: () => Promise.resolve(),
  mayAllow: true,
  addFiles: (files) => Promise.resolve({ added: files.length, failed: [] }),
  ...fields,
});

/** Routes that answer from the fakes above. */
export const fakeApi = (queue: Queue = printingQueue): QueueApi => ({
  readSummary: () => Promise.resolve(someQueues),
  readBoard: () => Promise.resolve({ queues: someBoard }),
  readQueue: () => Promise.resolve(queue),
  readHistory: () => Promise.resolve({ history: [...endedOf(queue)] }),
  addEntry: () => Promise.resolve({ ...queue, entries: [...queue.entries] }),
  removeEntry: () => Promise.resolve(queue),
  moveEntry: () => Promise.resolve(queue),
  startNext: () => Promise.resolve(queue),
  setReady: () => Promise.resolve(queue),
  clearHistory: () => Promise.resolve(queue),
  command: () => Promise.resolve(),
});

const devices = someQueues.printers.map((row) => ({
  id: row.device_id,
  name: row.printer.name,
}));

/**
 * Holds a session whose answers come from the fakes, for a story of a view
 * that reads it. The views read t, signal, store, principal, fleet,
 * namespace and pageHref of the host and nothing else.
 */
export const holdFakeSession = (api: QueueApi = fakeApi()): void => {
  const held: Record<string, string> = {};
  const grants: Grants = {
    allow: () => Promise.resolve(),
    disallow: () => Promise.resolve(),
    may: () => Promise.resolve(true),
  };
  holdSession({
    host: {
      t,
      signal: new AbortController().signal,
      principal: { id: ME, name: 'Alice', grants: [] },
      fleet: {
        devices,
        loading: false,
        loaded: true,
        error: undefined,
        device: (id: string) => devices.find((device) => device.id === id),
      },
      namespace: NAMESPACE,
      self: 'sink:test',
      pageHref: (path: string) => `/m/${NAMESPACE}/${path}`,
      store: {
        get: (key: string) => held[key],
        set: (key: string, value: string) => {
          held[key] = value;
        },
      },
    } as unknown as ModuleHost,
    api,
    grants,
    overview: new OverviewStore(api),
  });
};

const deviceFile = (path: string, folder = false) => ({
  name: path.slice(path.lastIndexOf('/') + 1),
  path,
  folder,
  read_only: false,
  size: folder ? 0 : 2_400_000,
  modified: '2026-10-28T16:30:00Z',
});

const storageEntry = (path: string, folder = false) => ({
  name: path.slice(path.lastIndexOf('/') + 1),
  path,
  folder,
  size: folder ? 0 : 1_800_000,
  modified: '2026-10-27T09:00:00Z',
});

const onPrinter: Record<string, ReturnType<typeof deviceFile>[]> = {
  '': [deviceFile('usb', true)],
  usb: [
    deviceFile('usb/models', true),
    deviceFile('usb/benchy.bgcode'),
    deviceFile('usb/bracket-0.4n-PETG.bgcode'),
    deviceFile('usb/prusa_printer_settings.ini'),
  ],
  'usb/models': [deviceFile('usb/models/gear.bgcode')],
};

/**
 * The core's file routes: a printer with a USB drive, and one local storage.
 * A folder the fakes do not know answers empty.
 */
export const fakeFilesApi = (): FilesApi =>
  ({
    listStorages: () =>
      Promise.resolve({
        items: [
          { name: 'local', display_name: 'This server', browse: true },
          { name: 'inbox', display_name: 'Upload only', browse: false },
        ],
      }),
    listDeviceFiles: ({ query }: { query?: { path?: string } }) => {
      const directory = query?.path ?? '';

      return Promise.resolve({ directory, items: onPrinter[directory] ?? [] });
    },
    listStorageFiles: ({ query }: { query?: { path?: string } }) => {
      const items =
        (query?.path ?? '') === ''
          ? [
              storageEntry('parts', true),
              storageEntry('lid-0.4n-PETG.bgcode'),
              storageEntry('hinge.gcode'),
            ]
          : [storageEntry('parts/clip.bgcode')];

      return Promise.resolve({ items });
    },
  }) as unknown as FilesApi;

/** A file picker over the fake routes, listing the printer's files. */
export const fakePicker = (): FilePickerView => {
  const picker = new FilePicker(fakeFilesApi(), 'mk4-1');
  void picker.start();

  return picker;
};

/** A board that holds the queues it is handed. */
export const boardView = (
  queues: readonly Queue[] = someBoard,
  load: Load = 'ready',
  fault?: FaultText,
): BoardView => ({
  load,
  fault,
  queues: load === 'ready' ? queues : [],
});
