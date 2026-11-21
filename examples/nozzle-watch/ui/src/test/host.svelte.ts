// A stub host, on the model of examples/spools/ui/src/test/host.ts: everything
// the module receives, and nothing it does not.
//
// Two pieces here are the real ones rather than fakes, and that is what makes
// the tests worth running. The registry is the library's own `SlotRegistry`,
// declared with the same `contributes` block `module.yaml` carries, so a
// registration outside the block is refused here exactly as the app refuses
// it. The fleet is a rune store with the same read-only surface the app hands
// a module, so a row replaced in a test moves the screen the way a frame from
// the printer does.
//
// The client is a real `PrusaConnect` over a fetch that refuses every call.
// This module needs no route, and a request that appears in it fails the test
// instead of passing unnoticed.
import { PrusaConnect } from '@prusa/connect';
import type { Device, ReadonlyFleet } from '@prusa/connect-ui/domain';
import { registerCatalogue, translateFor } from '@prusa/connect-ui/i18n';
import type { ModuleContributes, ModuleHost } from '@prusa/connect-ui/slots';
import {
  buildRegister,
  ComponentUnavailable,
  modulePageHref,
  SlotRegistry,
} from '@prusa/connect-ui/slots';
import type { Component } from 'svelte';
import { SvelteMap } from 'svelte/reactivity';

import english from '../locales/en.json';

/**
 * Where the host would install this module. The app derives it from the type
 * the operator declared, and a module never writes one (ADR-20260911-4p6 rule 15).
 */
export const NAMESPACE = 'com.github.aux.acme.nozzle-watch';

/**
 * The ceiling, written the way `../../module.yaml` declares it. The core
 * validates the manifest at install and answers this block on the resources
 * listing, so what the app declares to the registry is this and nothing else.
 */
export const CONTRIBUTES: ModuleContributes = {
  device_card: [{ id: 'nozzle', title: 'Nozzle' }],
  device_section: [{ id: 'nozzle', title: 'Nozzle temperature' }],
  device_tab: [{ id: 'nozzle', title: 'Nozzle' }],
  device_action: [
    {
      id: 'mark-changed',
      title: 'Mark nozzle changed',
      icon: 'wrench',
      destructive: false,
    },
  ],
};

/** One printer with every field the document declares, and a state tree a test fills. */
export const fakePrinter = (
  id: string,
  state: Record<string, unknown> = {},
): Device => ({
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
  link: {
    counted: false,
    dropped_telemetry: 0,
    dropped_telemetry_per_minute: 0,
    measured_over_seconds: 60,
    refused_frames: 0,
    refused_frames_per_minute: 0,
  },
  model: 'CORE One',
  name: id,
  profile_matched: true,
  serial: `serial-${id}`,
  state,
  unit_uid: `unit-${id}`,
  vendor: 'Prusa',
  verbs: null,
});

/**
 * The fleet, with the surface a module is handed and a setter the test writes
 * through. `rows` is `$state.raw` for the reason the library's own fleet gives:
 * a fleet runs to four hundred printers, every row is replaced whole, and deep
 * proxying each one buys nothing (svelte-5.md).
 */
export class FakeFleet implements ReadonlyFleet {
  #devices = $state.raw<readonly Device[]>([]);
  readonly loading = false;
  readonly loaded = true;
  readonly error = undefined;

  constructor(devices: readonly Device[] = []) {
    this.#devices = devices;
  }

  get devices(): readonly Device[] {
    return this.#devices;
  }

  /** The one writer. A module holds the read-only half and cannot reach this. */
  set devices(rows: readonly Device[]) {
    this.#devices = rows;
  }

  device(id: string): Device | undefined {
    return this.#devices.find((row) => row.id === id);
  }
}

/**
 * One DNS-1123 label, the shape the real `ModuleStore` holds a key to
 * (`packages/svelte/src/slots/module-store.svelte.ts` `KEY_PATTERN`): a key
 * with a colon or any other character outside it is refused there rather than
 * written, and this stub refuses it the same way so a bad key fails here
 * first.
 */
const KEY_PATTERN = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

/** The address one call went to, whichever of the three shapes fetch was given. */
const addressOf = (input: RequestInfo | URL): string => {
  if (typeof input === 'string') return input;

  return input instanceof URL ? input.href : input.url;
};

export interface Stub {
  host: ModuleHost;
  /** The fleet the test writes, which is the read-only one the module holds. */
  fleet: FakeFleet;
  /** The library's own registry, declared with the manifest's block. */
  registry: SlotRegistry;
  /** Every request the module made. It is meant to stay empty. */
  requests: string[];
}

/** The host a module receives, over the registry and the fleet a test drives. */
export const stubHost = (
  devices: readonly Device[] = [],
  components: Record<string, Component<never>> = {},
): Stub => {
  const registry = new SlotRegistry();
  registry.declare(NAMESPACE, CONTRIBUTES);

  // What the loader does before it imports a bundle: it reads the module's
  // own `locales/en.json` and merges it under the namespace
  // (frontend-i18n.md rule 7).
  registerCatalogue(NAMESPACE, 'en', english);

  const fleet = new FakeFleet(devices);
  const requests: string[] = [];
  // A plain map stands in for `ModuleStore`: `mark-changed` and `nozzle-tab`
  // only need one to agree with the other, and neither reaches the core.
  const records = new SvelteMap<string, string>();

  const fetcher: typeof globalThis.fetch = (input) => {
    requests.push(addressOf(input));

    return Promise.reject(
      new Error('this module reads the fleet and asks the core for nothing'),
    );
  };

  const host: ModuleHost = {
    client: new PrusaConnect({
      auth: { kind: 'session' },
      fetch: fetcher,
      retryDelay: 0,
    }),
    principal: { id: 'user:demo', grants: [] },
    fleet,
    t: translateFor(NAMESPACE),
    // A test never ends the module's time on the page, so nothing aborts it.
    signal: new AbortController().signal,
    namespace: NAMESPACE,
    self: 'sink:test',
    pageHref: (path) => modulePageHref(NAMESPACE, path),
    store: {
      get: (key) => records.get(key),
      set: (key, value) => {
        // The real store refuses the write rather than throwing: a pane that
        // asked for an impossible key keeps whatever it held before.
        if (!KEY_PATTERN.test(key)) return;
        records.set(key, value);
      },
      delete: (key) => {
        records.delete(key);
      },
      keys: () => [...records.keys()].sort(),
    },
    // The spools module is not installed here, unless a case hands a
    // component in through `components`.
    components: {
      load: (module, name) => {
        const held = components[`${module}/${name}`];

        return held === undefined
          ? Promise.reject(new ComponentUnavailable('absent', module, name))
          : Promise.resolve(held as never);
      },
    },
    register: buildRegister({
      page: (path, component) => {
        registry.registerPage(NAMESPACE, path, component);
      },
      section: (slot, id, component) => {
        registry.registerSection(NAMESPACE, slot, id, component);
      },
      action: (slot, id, run) => {
        registry.registerAction(NAMESPACE, slot, id, run);
      },
      component: (name) => {
        throw new Error(
          `nozzle-watch exports no component, and registered ${name}`,
        );
      },
    }),
  };

  return { host, fleet, registry, requests };
};
