// Fakes the store and loader tests are built from. They live under `src/test`,
// which `package.json` keeps out of the published files.
//
// Nothing here opens a socket. The SDK's own tests fake `fetch` under the
// transport (`packages/typescript/test/stub.ts`); the library holds no
// transport, so what it fakes is the operations themselves.
import type { components, PrusaConnect } from '@prusa/connect';

import type { FleetClient } from '../data/fleet.svelte';
import type { FleetListingQuery } from '../data/fleet-listing.svelte';
import type { Device } from '../domain/command';
import type { ListingStore } from '../primitives';
import type { DocumentLoad, RouterNavigation } from '../router/router.svelte';
import type { FleetListingSource } from '../shell/printers/types';
import type { TagFleetSource } from '../shell/tags/types';
import type { ModuleLoader } from '../slots/loader.svelte';
import type { ModuleStorage } from '../slots/module-store.svelte';
import type { ModuleUI } from '../slots/types';

/** One printer with every field the document declares, and nothing interesting in it. */
export const fakeDevice = (id: string, name = id): Device => ({
  actions: [],
  authorization_epoch: 1,
  capabilities: [],
  capability_epoch: 1,
  connected: true,
  driver: 'fake',
  firmware: '1.0.0',
  id,
  issues: [],
  kind: 'printer',
  labels: {},
  tags: [],
  verbs: null,
  link: {
    counted: false,
    dropped_telemetry: 0,
    dropped_telemetry_per_minute: 0,
    measured_over_seconds: 60,
    refused_frames: 0,
    refused_frames_per_minute: 0,
  },
  model: 'CORE One',
  name,
  profile_matched: true,
  serial: `serial-${id}`,
  state: {},
  unit_uid: `unit-${id}`,
  vendor: 'Prusa',
});

/** The members of a device page that no test here varies. */
export const FAKE_PAGE_REST: Omit<
  components['schemas']['DevicePage'],
  'items' | 'limit' | 'offset' | 'total'
> = {
  filters: [],
  issues: {
    affected: 0,
    by_severity: { error: 0, warning: 0, info: 0 },
    counts: {},
  },
  order: 'asc',
  sort: 'urgency',
  sorts: [],
};

export interface FleetCalls {
  list: number;
  get: string[];
}

/**
 * A fleet client that answers from the rows it was given. `calls` records what
 * it was asked, which is what a one-flight test reads.
 */
export const fakeFleetClient = (
  rows: readonly Device[],
): { client: FleetClient; calls: FleetCalls } => {
  const calls: FleetCalls = { list: 0, get: [] };
  const client: FleetClient = {
    listDevices: async () => {
      calls.list += 1;
      const items = [...rows];

      return Promise.resolve({
        ...FAKE_PAGE_REST,
        items,
        limit: items.length,
        offset: 0,
        total: items.length,
      });
    },
    getDevice: async (request) => {
      const id = request.path.device_id;
      calls.get.push(id);
      const found = rows.find((row) => row.id === id);
      if (!found) throw new Error(`no device ${id}`);

      return Promise.resolve(found);
    },
    events: () => {
      throw new Error('this fake opens no stream');
    },
  };

  return { client, calls };
};

/** One instance of the resources listing, carrying a `ui` object. */
export interface FakeModule {
  namespace: string;
  ui: ModuleUI;
  hidden_contributions?: string[];
  /** The release the manifest declared. */
  release?: string;
}

/**
 * A client the loader can read. Only three operations are answered, so the
 * cast names the type without writing out every operation the core declares;
 * calling any other one is a test bug and fails loudly.
 *
 * `who` may be a function, so a test changes who is signed in between runs.
 */
export const fakeLoaderClient = (
  modules: readonly FakeModule[],
  who: string | (() => string) = 'user:alice',
): PrusaConnect => {
  const principalOf = (): string => (typeof who === 'string' ? who : who());
  const client = {
    listResources: () =>
      Promise.resolve({
        items: modules.map((module) => ({
          config: {},
          enabled: true,
          generation: 1,
          kind: 'sink',
          name: module.namespace,
          namespace: module.namespace,
          required: false,
          runtime: 'asset' as const,
          source: 'file' as const,
          status: {
            attempt: 0,
            conditions: [],
            dropped: 0,
            observed_generation: 1,
            serving: true,
            state: 'ready' as const,
          },
          type: module.namespace,
          ui: module.ui,
          ...(module.release === undefined
            ? {}
            : { module: { version: module.release } }),
          hidden_contributions: module.hidden_contributions,
          uid: `uid-${module.namespace}`,
          version: '1',
        })),
      }),
    getPrincipal: () => Promise.resolve({ principal: principalOf() }),
    listSubjectGrants: () =>
      Promise.resolve({ devices: [], subject: principalOf() }),
  };

  // The loader reaches three operations and a module receives the whole
  // client; a fake of all of `PrusaConnect` cannot be written out, so the cast
  // is what names the type here.
  // A run binds the client to its signal. The fake answers itself, so what a
  // test adds to it later is seen by every run.
  Object.assign(client, { withSignal: () => client });

  return client as unknown as PrusaConnect;
};

/**
 * A `ModuleLoader` that is ready from the start and whose `activate` resolves
 * at once. `ModuleLoader` holds private fields, so no object literal is
 * structurally one; the cast is what names the type here, as for the other
 * fakes above.
 */
export const readyLoader = (): ModuleLoader =>
  ({
    ready: true,
    activate: () => Promise.resolve(),
  }) as unknown as ModuleLoader;

/** A `localStorage` that lives in a Map, so a test reads what was written. */
export const fakeStorage = (
  seed: Record<string, string> = {},
): { storage: ModuleStorage; map: Map<string, string> } => {
  const map = new Map(Object.entries(seed));
  const storage: ModuleStorage = {
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => {
      map.set(key, value);
    },
    removeItem: (key) => {
      map.delete(key);
    },
    get length() {
      return map.size;
    },
    key: (index) => [...map.keys()][index] ?? null,
  };

  return { storage, map };
};

/**
 * A stand-in for the browser's address bar and history, so a test can
 * navigate without writing the page's real one. `back()` does what the back
 * button does: it moves the address and then fires `popstate`.
 *
 * The events are the page's own. A click on a link bubbles to the window, and
 * a router that listened anywhere else would never hear one, so this fake
 * registers every listener there and fires `popstate` there too. Only the
 * address bar and the history are pretended.
 */
export class FakeNavigation implements RouterNavigation {
  readonly origin = globalThis.location.origin;
  #entries: readonly string[];
  #at: number;

  constructor(path = '/') {
    this.#entries = [path];
    this.#at = 0;
  }

  get pathname(): string {
    return (this.#entries[this.#at] ?? '/').replace(/[?#].*$/, '');
  }

  get search(): string {
    const entry = (this.#entries[this.#at] ?? '').replace(/#.*$/, '');
    const at = entry.indexOf('?');

    return at < 0 ? '' : entry.slice(at);
  }

  get hash(): string {
    const entry = this.#entries[this.#at] ?? '';
    const at = entry.indexOf('#');

    return at < 0 ? '' : entry.slice(at);
  }

  /** How many times a move asked for the top of the page. */
  scrolledToTop = 0;

  scrollToTop(): void {
    this.scrolledToTop += 1;
  }

  /** Every address this fake holds, oldest first. */
  get entries(): readonly string[] {
    return this.#entries;
  }

  pushState(_data: unknown, _unused: string, url: string): void {
    // A push after a back drops what was ahead, which is what a browser does.
    this.#entries = [...this.#entries.slice(0, this.#at + 1), url];
    this.#at = this.#entries.length - 1;
  }

  replaceState(_data: unknown, _unused: string, url: string): void {
    this.#entries = this.#entries.map((entry, index) =>
      index === this.#at ? url : entry,
    );
  }

  /** Every address handed to the browser as a document of its own, with how. */
  readonly loads: [string, DocumentLoad][] = [];

  load(url: string, how: DocumentLoad): void {
    this.loads.push([url, how]);
  }

  back(): void {
    if (this.#at === 0) return;
    this.#at -= 1;
    globalThis.dispatchEvent(new Event('popstate'));
  }

  addEventListener(
    type: 'popstate' | 'click',
    listener: (event: Event) => void,
  ): void {
    globalThis.addEventListener(type, listener);
  }

  removeEventListener(
    type: 'popstate' | 'click',
    listener: (event: Event) => void,
  ): void {
    globalThis.removeEventListener(type, listener);
  }
}

/**
 * A fleet a screen test drives directly, standing in for the `Fleet` store
 * without a client under it.
 */
export const fakeFleetSource = (
  devices: readonly Device[],
  over: Partial<{ loading: boolean; loaded: boolean; error: Error }> = {},
): TagFleetSource => ({
  devices,
  loading: over.loading ?? false,
  loaded: over.loaded ?? true,
  error: over.error,
});

type DeviceFilter = components['schemas']['DeviceFilter'];
type DeviceSort = components['schemas']['DeviceSort'];

/** The `state` filter the server advertises, each value carrying one printer. */
export const stateFilter = (values: readonly string[]): DeviceFilter => ({
  param: 'state',
  kind: 'multi',
  values: values.map((value) => ({ value, count: 1 })),
});

export const searchFilter = (maxLength = 128): DeviceFilter => ({
  param: 'search',
  kind: 'text',
  max_length: maxLength,
});

/** A sort the server advertises; `last_seen` reads newest first, as the server has it. */
export const sortOf = (field: DeviceSort['field']): DeviceSort => ({
  field,
  default_order: field === 'last_seen' ? 'desc' : 'asc',
});

/**
 * A listing a screen test drives directly, standing in for the `FleetListing`
 * store without a client under it. `asked` records every query the screen set.
 */
export const fakeListing = (
  over: Partial<Omit<FleetListingSource, 'setQuery'>> = {},
): FleetListingSource & { asked: Partial<FleetListingQuery>[] } => {
  const asked: Partial<FleetListingQuery>[] = [];
  const items = over.items ?? [];

  return {
    items,
    total: items.length,
    filters: [],
    sorts: [],
    sort: 'urgency',
    order: 'asc',
    issues: FAKE_PAGE_REST.issues,
    loading: false,
    loaded: true,
    error: undefined,
    query: {
      page: 0,
      pageSize: 50,
      filters: {},
      search: '',
      sort: undefined,
      order: undefined,
    },
    ...over,
    asked,
    setQuery: (next) => {
      asked.push(next);
    },
  };
};

/** A listing store that forgets, so one test's choice never reaches the next. */
export const fakeListingStore = (): ListingStore => ({
  read: () => null,
  write: () => undefined,
});
