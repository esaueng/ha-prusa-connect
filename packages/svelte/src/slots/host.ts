// The one object a module's bundle receives, and the helper it declares its
// setup function with.
//
// `defineModule` is pull request 185's `definePlugin`, moved from
// packages/sdk/src/plugin.ts on 2026-09-11: an identity function that gives a
// module author the types without an import of the host. What changed is what
// the object carries. PR 185 handed a plugin `devices.list`, `devices.watch`
// and a mediated `fetch`; ADR-20260911-4p6 rules 18 and 20 hand it the SDK in session
// mode instead, and no `fetch` and no token at all. What a bundle may reach
// is what the person may reach, and the core authorizes every request.
import type { PrusaConnect } from '@prusa/connect';
import type { Component } from 'svelte';

import type { ReadonlyFleet } from '../domain/fleet';
import type { ModulePrincipal } from '../domain/principal';
import type { Translate } from '../i18n';
import type { ModuleRegister } from './generated';
import type { ScopedStore } from './module-store.svelte';
import type { StoredFileRef } from './registry.svelte';

/** Why `host.components.load` could not answer a component. */
export type ComponentReason =
  /** The importing module's manifest does not name the component under ui.imports. */
  | 'undeclared'
  /** No module of that namespace is in the listing for this person, or the app may not load it. */
  | 'absent'
  /** The module is there and its manifest exports no component of that name. */
  | 'not-exported'
  /** The module's bundle ran and registered no component of that name. */
  | 'unregistered'
  /** The module's bundle was refused or threw while it loaded. */
  | 'failed'
  /** The call was made while the importing module's own setup was still running. */
  | 'setup';

/**
 * The refusal `host.components.load` rejects with. A module draws its own
 * fallback for it, and `ModuleComponent` does that from a snippet.
 */
export class ComponentUnavailable extends Error {
  readonly reason: ComponentReason;
  readonly module: string;
  readonly component: string;

  constructor(reason: ComponentReason, module: string, component: string) {
    super(`${module}/${component} is not available: ${reason}`);
    this.name = 'ComponentUnavailable';
    this.reason = reason;
    this.module = module;
    this.component = component;
  }
}

/**
 * The components of other modules a bundle draws (ADR-20261027-wvj rules 20
 * to 23).
 */
export interface ModuleComponents {
  /**
   * The component `name` of the module `module`, once that module's bundle
   * has registered it. The import must be under ui.imports in this module's
   * manifest. Call it from a component, never from setup: the other bundle
   * may load after this one.
   *
   * The props are the exporting module's to publish, typically in its
   * client package, and the caller names them as `Props`.
   */
  load<Props extends Record<string, unknown>>(
    module: string,
    name: string,
  ): Promise<Component<Props>>;
}

/**
 * Components of no other module: every load is refused as absent. A test
 * host, or an app that runs one module alone, hands this in.
 */
export const noComponents: ModuleComponents = {
  load: (module, name) =>
    Promise.reject(new ComponentUnavailable('absent', module, name)),
};

/**
 * What a module's default export receives, and it holds nothing else
 * (ADR-20260911-4p6 rule 18).
 */
export interface ModuleHost {
  /** The app's `PrusaConnect`, in session mode. A module's own typed client binds onto it. */
  readonly client: PrusaConnect;
  /** Who is signed in, so a page can hide what the person may not do. */
  readonly principal: ModulePrincipal;
  /** The registration functions, held to `contributes` and `exports`. */
  readonly register: ModuleRegister;
  /** The components of other modules this bundle draws (ADR-20261027-wvj). */
  readonly components: ModuleComponents;
  /** A key-value store scoped to this module's namespace. */
  readonly store: ScopedStore;
  /** The library's fleet store, read-only. The fleet is loaded once, however many screens show it. */
  readonly fleet: ReadonlyFleet;
  /**
   * Every text this module draws, in the language the person reads. It is
   * bound to the module's namespace: `host.t('spools.title')` reads the
   * module's own `locales/<lang>.json` first and the core's catalogue after
   * it, so a module may name a key the core already names.
   *
   * No page in a module writes an English string.
   * `.agents/rules/frontend-i18n.md` rule 1 holds for a module's bundle
   * exactly as it holds for the core's own screens.
   */
  readonly t: Translate;
  /**
   * Aborted when this module stops belonging to the page: the person who
   * signed in leaves, or the app reloads its modules. A module closes the
   * streams and timers it opened and forgets what it holds when it fires.
   * The next person gets a new setup call with a new host.
   */
  readonly signal: AbortSignal;
  /**
   * The namespace this module was installed under, such as the one its routes
   * are served at. A module never reads it out of an address.
   */
  readonly namespace: string;
  /**
   * Who this module's own process is to the server's grants: its instance's
   * kind and name, such as `sink:simple-queue`. A page writes a grant to it
   * to let the module act by itself. It is not the person on the page, who
   * is `principal`.
   */
  readonly self: string;
  /**
   * The address of one of this module's own pages, for a link from a card or
   * a section to it: `/m/<namespace>/<path>` (ADR-20260911-4p6 rule 9). A module does
   * not know the namespace it was installed under, so the host writes it.
   */
  pageHref(path: string): string;
  /**
   * Sends files from the person's computer to a printer the way a file
   * dropped on that printer's page is sent: the page asks whether to keep a
   * copy in a storage of this server, with the module's own entry of the
   * device-upload-action slot picked, and that entry runs on each file while
   * it is on its way. `action` is the id of that entry. It works while the
   * printer's page is open, such as from a tab of it. A core without it
   * leaves it out, so a module checks for it.
   */
  uploadToPrinter?(
    deviceId: string,
    files: readonly File[],
    action: string,
  ): void;
  /**
   * Copies files from a storage of this server to a printer, as the
   * printer's page copies one, and runs the module's own entry of the
   * device-upload-action slot on each while it is on its way. A name the
   * printer already holds is asked about on that page. It works while the
   * printer's page is open. A core without it leaves it out.
   */
  sendToPrinter?(
    deviceId: string,
    files: readonly StoredFileRef[],
    action: string,
  ): void;
}

/** What a bundle default-exports. */
export type ModuleSetup = (host: ModuleHost) => void | Promise<void>;

/**
 * Declares a module's setup function. It returns what it was given: its only
 * job is to type the host object at the author's call site.
 *
 * ```ts
 * export default defineModule((host) => {
 *   host.register.page('spools', SpoolsPage);
 * });
 * ```
 */
export const defineModule = (setup: ModuleSetup): ModuleSetup => setup;
