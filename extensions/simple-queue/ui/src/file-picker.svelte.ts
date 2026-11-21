// What the Add files dialog shows: the places files come from, one folder of
// the place picked, and the files ticked so far. A tick is kept while the
// person moves to another folder or place, so files from several folders go
// on the queue together.
import { describeFault, type FaultText } from '@prusa/connect-ui/i18n';

import type { FileAt } from './adding';
import {
  fileAtOf,
  filesUnder,
  keyOf,
  type FilesApi,
  PRINTER,
  type Place,
  placesOf,
  type Row,
  rowsOf,
} from './files';

/** How a read of the place ended. */
export type PickerLoad = 'loading' | 'ready' | 'failed';

/** Every print file of the place in one list, or one folder at a time. */
export type PickerView = 'all' | 'folders';

/** One ticked file, with the name the dialog lists it under. */
export interface Picked {
  key: string;
  name: string;
  file: FileAt;
}

/** What the dialog draws, and what a story hands it in place of the server. */
export interface FilePickerView {
  /** The printer the files are picked for. */
  readonly device: string;
  readonly places: readonly Place[];
  readonly place: Place;
  readonly directory: string;
  /** The folder the place opens on: its only storage, or '' for its top. */
  readonly root: string;
  readonly rows: readonly Row[];
  readonly view: PickerView;
  /** Whether the list of every file left some folders unread. */
  readonly partial: boolean;
  readonly load: PickerLoad;
  readonly fault: FaultText | undefined;
  readonly picked: readonly Picked[];
  /** Reads the places and lists the printer's files. */
  start(): Promise<void>;
  choose(place: Place): Promise<void>;
  show(view: PickerView): Promise<void>;
  open(directory: string): Promise<void>;
  isPicked(row: Row): boolean;
  toggle(row: Row): void;
  dispose(): void;
}

export class FilePicker implements FilePickerView {
  places = $state.raw<readonly Place[]>([PRINTER]);
  place = $state.raw<Place>(PRINTER);
  directory = $state('');
  root = $state('');
  rows = $state.raw<readonly Row[]>([]);
  view = $state<PickerView>('all');
  partial = $state(false);
  load = $state<PickerLoad>('loading');
  fault = $state<FaultText | undefined>(undefined);
  picked = $state.raw<readonly Picked[]>([]);

  readonly #api: FilesApi;
  readonly device: string;
  #controller = new AbortController();
  // Only the newest read draws, so a fast click on two folders shows the second.
  #read = 0;

  constructor(api: FilesApi, device: string) {
    this.#api = api;
    this.device = device;
  }

  async start(): Promise<void> {
    // A server whose storages cannot be listed still offers the printer.
    try {
      this.places = await placesOf(this.#api, this.#controller.signal);
    } catch {
      this.places = [PRINTER];
    }
    await this.show(this.view);
  }

  choose(place: Place): Promise<void> {
    this.place = place;
    this.root = '';

    return this.show('all');
  }

  show(view: PickerView): Promise<void> {
    this.view = view;

    return view === 'all' ? this.#all() : this.open(this.root);
  }

  async #all(): Promise<void> {
    await this.#load('', async (signal) => {
      const found = await filesUnder(
        this.#api,
        this.device,
        this.place,
        signal,
      );

      return () => {
        this.root = found.root;
        this.partial = found.partial;
        this.rows = found.rows;
      };
    });
  }

  async open(directory: string): Promise<void> {
    let only: Row | undefined;
    await this.#load(directory, async (signal) => {
      const rows = await rowsOf(
        this.#api,
        this.device,
        this.place,
        directory,
        signal,
      );
      // A place whose top holds one folder and nothing else, a printer with
      // one storage, opens on that folder.
      only = directory === '' && rows.length === 1 ? rows[0] : undefined;

      return () => {
        this.rows = rows;
      };
    });
    if (only?.folder && this.directory === '') {
      this.root = only.path;
      await this.open(only.path);
    }
  }

  // Reads one list and draws it, unless a newer read started meanwhile.
  async #load(
    directory: string,
    read: (signal: AbortSignal) => Promise<() => void>,
  ): Promise<void> {
    const turn = ++this.#read;
    const signal = this.#controller.signal;
    this.directory = directory;
    this.load = 'loading';
    try {
      const draw = await read(signal);
      if (turn !== this.#read) return;
      draw();
      this.load = 'ready';
      this.fault = undefined;
    } catch (thrown) {
      if (signal.aborted || turn !== this.#read) return;
      this.rows = [];
      this.load = 'failed';
      this.fault = describeFault(thrown);
    }
  }

  #keyOf(row: Row): string {
    return `${keyOf(this.place)}:${row.path}`;
  }

  isPicked(row: Row): boolean {
    const key = this.#keyOf(row);

    return this.picked.some((one) => one.key === key);
  }

  toggle(row: Row): void {
    if (row.folder) return;
    const key = this.#keyOf(row);
    this.picked = this.isPicked(row)
      ? this.picked.filter((one) => one.key !== key)
      : [
          ...this.picked,
          { key, name: row.name, file: fileAtOf(this.place, row) },
        ];
  }

  dispose(): void {
    this.#controller.abort();
  }
}
