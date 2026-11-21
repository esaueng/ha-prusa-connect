// What the host knows a module has contributed, and why a module is not
// there.
//
// Moved from pull request 185, packages/core/src/plugins/registry.svelte.ts,
// 2026-09-11. Two things come across whole: a registration the manifest did
// not declare is refused, and one module's registrations are forgotten
// together. What is new is the four slots of ADR-20260911-4p6 rule 9 in place of
// pages and widgets, and refusals held as state (rule 26) rather than thrown
// away.
//
// A registration is refused unless the module's `contributes` block declared
// it. That is the point of having a manifest at all: an operator can read
// what a module will add before installing it, and that is not true if the
// running code can add a pane the block never named.
import type { Component } from 'svelte';
import { SvelteSet } from 'svelte/reactivity';

import { t } from '../i18n';
import { modulePageHref } from '../module-href';
import { cardDeclarationProblem, readCardDeclaration } from './card-settings';
import { ownedBy } from './component-owner';
import type {
  CardDeclaration,
  CardOptions,
  CardProps,
  DeclaredCard,
  RegisteredCard,
} from './card-types';
import { CORE_CARD_NAMESPACE } from './card-types';
import type { DeviceSelector } from './device-selector';
import type {
  ActionRuns,
  ActionSlot,
  Icon,
  SectionProps,
  SectionSlot,
} from './generated';
import { SLOT_KEYS } from './generated';
import type { NavItem } from './navigation';
import type { ModuleContributes, SlotName } from './types';

/** A page the host admitted, with the namespace it was installed under. */
export interface RegisteredPage {
  namespace: string;
  path: string;
  title: string;
  icon: string | undefined;
  hidden: boolean;
  /** A page takes no props: the route is the whole context. */
  component: Component;
}

/** A section the host admitted, in the section slot it was registered into. */
export interface RegisteredSection {
  namespace: string;
  slot: SectionSlot;
  id: string;
  title: string;
  /** The file types the entry is drawn for, on a slot handed a file. Absent is every file. */
  fileTypes: readonly string[] | undefined;
  /** On a device slot, the devices the entry is drawn for. Absent is every device (ADR-20261110-5np). */
  devices: DeviceSelector | undefined;
  component: Component<SectionProps[SectionSlot]>;
}

/** An action the host admitted, in the action slot it was registered into. */
export interface RegisteredAction {
  namespace: string;
  slot: ActionSlot;
  id: string;
  title: string;
  icon: Icon | undefined;
  destructive: boolean;
  /** The file types the entry is drawn for, on a slot handed a file. Absent is every file. */
  fileTypes: readonly string[] | undefined;
  /** A button on the subject's own page rather than a menu item (ADR-20261029-dgv). */
  primary: boolean;
  /** On a device slot, the devices the entry is drawn for. Absent is every device (ADR-20261110-5np). */
  devices: DeviceSelector | undefined;
  run: ActionRuns[ActionSlot];
}

/** A component a module handed other modules' bundles (ADR-20261027-wvj rule 21). */
export interface RegisteredComponent {
  namespace: string;
  name: string;
  component: Component<never>;
}

/** One entry a module's manifest declared, before any bundle registers it. */
export interface DeclaredEntry {
  namespace: string;
  id: string;
  title: string;
  /** The file types the entry is drawn for, on a slot handed a file. Absent is every file. */
  fileTypes?: readonly string[] | undefined;
  /** On a device slot, the devices the entry is drawn for. Absent is every device (ADR-20261110-5np). */
  devices?: DeviceSelector | undefined;
  /**
   * On a printer tab, whether the manifest asks for the tab's page to be
   * cross-origin isolated. Absent is not isolated.
   */
  crossOriginIsolated?: boolean | undefined;
}

/** A declared entry of an action slot, with the fields only an action carries. */
export interface DeclaredAction extends DeclaredEntry {
  icon: Icon | undefined;
  destructive: boolean;
  /** A button on the subject's own page rather than a menu item (ADR-20261029-dgv). */
  primary: boolean;
}

/**
 * The array one manifest key holds, read as the shape a section or an action
 * entry carries. The cast is what narrows `ModuleContributes`'s per-slot
 * field to this shape, because the manifest's generated type carries one
 * concrete array type per key and not a structural shape every list shares.
 */
const entriesOf = (
  contributes: ModuleContributes | undefined,
  key: keyof ModuleContributes,
): readonly {
  id: string;
  title: string;
  icon?: string;
  destructive?: boolean;
  primary?: boolean;
  file_types?: string[];
  devices?: DeviceSelector;
}[] =>
  (contributes?.[key] as
    | readonly {
        id: string;
        title: string;
        icon?: string;
        destructive?: boolean;
        primary?: boolean;
        file_types?: string[];
        devices?: DeviceSelector;
      }[]
    | undefined) ?? [];

/**
 * Why a module is not on the screen. It is state a settings screen reads and
 * never a console line, because "it is installed and it is not running" is
 * the question an operator asks first (ADR-20260911-4p6 rule 26).
 */
export type SlotRefusal =
  | {
      namespace: string;
      reason: 'undeclared';
      slot: SlotName | 'component';
      /** The path or id the module tried to register. */
      name: string;
    }
  | {
      namespace: string;
      reason: 'library-mismatch';
      /** The range the bundle declared. */
      range: string;
      /** The version this library is. */
      version: string;
    }
  | { namespace: string; reason: 'threw'; detail: string };

/** One line a settings screen can show beside the module's name. */
export const describeRefusal = (refusal: SlotRefusal): string => {
  switch (refusal.reason) {
    case 'undeclared':
      return t('refusal.undeclared', {
        slot: refusal.slot,
        name: refusal.name,
      });
    case 'library-mismatch':
      return t('refusal.libraryMismatch', {
        range: refusal.range,
        version: refusal.version,
      });
    case 'threw':
      return t('refusal.threw', { detail: refusal.detail });
  }
};

/** Thrown at the module, so a bundle that oversteps is dropped whole. */
export class UndeclaredRegistration extends Error {
  constructor(namespace: string, slot: SlotName | 'component', name: string) {
    super(
      `${namespace} registered a ${slot} its manifest does not declare: ${name}`,
    );
    this.name = 'UndeclaredRegistration';
  }
}

/** The cards one manifest declared, each one read as the page holds it. */
const cardsOf = (
  contributes: ModuleContributes | undefined,
): readonly CardDeclaration[] =>
  (contributes?.cards ?? []).flatMap((wire) => {
    const declaration = readCardDeclaration(wire);
    return declaration === undefined ? [] : [declaration];
  });

const sectionOf = (
  sections:
    | readonly {
        id: string;
        title: string;
        file_types?: string[];
        devices?: DeviceSelector;
      }[]
    | null
    | undefined,
  id: string,
):
  | { title: string; file_types?: string[]; devices?: DeviceSelector }
  | undefined => (sections ?? []).find((section) => section.id === id);

/**
 * Files a module hands to the page of one printer, to be sent there the way a
 * file dropped on that page is, with the module's device-upload-action entry
 * running on each while it is on its way. `files` come from the person's
 * computer and are asked about first; `stored` are files in a storage of
 * this server and go at once.
 */
export interface UploadRequest {
  readonly namespace: string;
  /** The id of the module's device-upload-action entry. */
  readonly action: string;
  readonly deviceId: string;
  readonly files: readonly File[];
  readonly stored?: readonly StoredFileRef[] | undefined;
}

/** A file in a storage of this server. */
export interface StoredFileRef {
  readonly storage: string;
  readonly path: string;
}

/**
 * Reactive lists and nothing else. A component reading `registry.pages()`
 * inside a `$derived` re-runs when a module registers, so the registry needs
 * no listener list of its own.
 *
 * It is a class because it is shared state (svelte-5.md). An app builds one
 * and puts it in context, so two apps on one page, or a server render, do not
 * share one set of registrations.
 */
export class SlotRegistry {
  // Files modules asked to send to a printer, oldest first, waiting for that
  // printer's page to ask the person how.
  #uploads = $state.raw<readonly UploadRequest[]>([]);
  // The printers whose page is open. Only an open page asks, so a request
  // for any other printer is dropped and never waits for a later visit.
  readonly #uploadPages = new SvelteSet<string>();
  #pages = $state<RegisteredPage[]>([]);
  #sections = $state<RegisteredSection[]>([]);
  #actions = $state<RegisteredAction[]>([]);
  #cards = $state<RegisteredCard[]>([]);
  // The core's own cards outlive `clear` and `forget`: they belong to no
  // module and to no person, and the core registers them once.
  #coreCards = $state<RegisteredCard[]>([]);
  #refusals = $state<SlotRefusal[]>([]);
  #components = $state<RegisteredComponent[]>([]);
  // The component names each module's manifest exports.
  #exports = $state<Record<string, readonly string[]>>({});
  // Tabs and menus read what a module declared before it registers anything,
  // so this is state and not a plain field.
  #declared = $state<Record<string, ModuleContributes>>({});
  // `<slot key>/<identity>` entries an operator hid, per namespace.
  #hidden = $state<Record<string, readonly string[]>>({});

  /**
   * Says the page of a printer is open, and answers how to say it closed.
   * Closing drops what still waits for that printer.
   */
  openUploads(deviceId: string): () => void {
    this.#uploadPages.add(deviceId);

    return () => {
      this.#uploadPages.delete(deviceId);
      this.#uploads = this.#uploads.filter(
        (request) => request.deviceId !== deviceId,
      );
    };
  }

  /**
   * Holds a module's files for the page of the printer they go to, behind
   * any request already waiting. It answers false, and holds nothing, when
   * that page is not open.
   */
  requestUpload(request: UploadRequest): boolean {
    if (!this.#uploadPages.has(request.deviceId)) return false;
    this.#uploads = [...this.#uploads, request];

    return true;
  }

  /** The oldest request waiting for one printer. */
  uploadFor(deviceId: string): UploadRequest | undefined {
    return this.#uploads.find((request) => request.deviceId === deviceId);
  }

  /** Hands the oldest request waiting for one printer to its page, once. */
  takeUpload(deviceId: string): UploadRequest | undefined {
    const request = this.uploadFor(deviceId);
    this.#uploads = this.#uploads.filter((held) => held !== request);

    return request;
  }

  /**
   * Records what one module's manifest allows. Nothing registers until this
   * has run, so a module the host never admitted can register nothing.
   *
   * A hidden entry stays declared, so the bundle may register it, but no
   * query returns it.
   */
  declare(
    namespace: string,
    contributes: ModuleContributes,
    hidden: readonly string[] = [],
  ): void {
    this.#declared[namespace] = contributes;
    this.#hidden[namespace] = hidden;
  }

  /**
   * Records the component names one module's manifest exports. A bundle
   * registers no other name (ADR-20261027-wvj rule 21).
   */
  declareExports(namespace: string, names: readonly string[]): void {
    this.#exports[namespace] = names;
  }

  /** Whether a module's manifest exports a component of this name. */
  exports(namespace: string, name: string): boolean {
    return this.#exports[namespace]?.includes(name) ?? false;
  }

  /**
   * A component a module hands other bundles, refused unless its manifest
   * exports that name. A name registered twice throws, as a card does.
   */
  registerComponent(
    namespace: string,
    name: string,
    component: Component<never>,
  ): void {
    if (!this.exports(namespace, name)) {
      this.#refusals.push({
        namespace,
        reason: 'undeclared',
        slot: 'component',
        name,
      });
      throw new UndeclaredRegistration(namespace, 'component', name);
    }
    if (this.component(namespace, name) !== undefined) {
      throw new Error(`${namespace}/${name} is registered twice`);
    }

    this.#components.push({ namespace, name, component });
    ownedBy(component, namespace);
  }

  /** The component a module registered under a name, or undefined. */
  component(namespace: string, name: string): Component<never> | undefined {
    return this.#components.find(
      (held) => held.namespace === namespace && held.name === name,
    )?.component;
  }

  /** Whether the loader refused a module or dropped it after it threw. */
  refused(namespace: string): boolean {
    return this.#refusals.some((refusal) => refusal.namespace === namespace);
  }

  /**
   * Replaces the entries an operator hid for one module, so a change written
   * from the extension's page shows without reloading the app.
   */
  hide(namespace: string, hidden: readonly string[]): void {
    this.#hidden[namespace] = hidden;
  }

  #isHidden(namespace: string, key: string, identity: string): boolean {
    return this.#hidden[namespace]?.includes(`${key}/${identity}`) ?? false;
  }

  registerPage(namespace: string, path: string, component: Component): void {
    const declared = (this.#declared[namespace]?.pages ?? []).find(
      (page) => page.path === path,
    );
    if (!declared) {
      this.#refusals.push({
        namespace,
        reason: 'undeclared',
        slot: 'page',
        name: path,
      });

      throw new UndeclaredRegistration(namespace, 'page', path);
    }

    this.#pages.push({
      namespace,
      path: declared.path,
      title: declared.title,
      icon: declared.icon,
      hidden: declared.hidden,
      component,
    });
  }

  registerSection<S extends SectionSlot>(
    namespace: string,
    slot: S,
    id: string,
    component: Component<SectionProps[S]>,
  ): void {
    const declared = sectionOf(
      this.#declared[namespace]?.[SLOT_KEYS[slot]],
      id,
    );
    if (declared === undefined) {
      this.#refusals.push({ namespace, reason: 'undeclared', slot, name: id });
      throw new UndeclaredRegistration(namespace, slot, id);
    }

    // The row keeps the component as the widest section component; the
    // outlet hands it the props its slot's subject names.
    this.#sections.push({
      namespace,
      slot,
      id,
      title: declared.title,
      fileTypes: declared.file_types,
      devices: declared.devices,
      component: component as Component<SectionProps[SectionSlot]>,
    });
  }

  registerAction<S extends ActionSlot>(
    namespace: string,
    slot: S,
    id: string,
    run: ActionRuns[S],
  ): void {
    const entry = entriesOf(this.#declared[namespace], SLOT_KEYS[slot]).find(
      (action) => action.id === id,
    );
    if (entry === undefined) {
      this.#refusals.push({ namespace, reason: 'undeclared', slot, name: id });
      throw new UndeclaredRegistration(namespace, slot, id);
    }

    this.#actions.push({
      namespace,
      slot,
      id,
      title: entry.title,
      // A declared icon is a plain string off the wire; the menu falls back
      // to a default one for a name the icon set does not carry.
      icon: entry.icon as Icon | undefined,
      destructive: entry.destructive ?? false,
      fileTypes: entry.file_types,
      primary: entry.primary ?? false,
      devices: entry.devices,
      run,
    });
  }

  /**
   * A module's card, refused unless its manifest declared a card of that id.
   * The declaration the card is drawn with is the manifest's, so the bundle
   * cannot change its size or its schema. A card registered twice throws, as
   * a core card does.
   */
  registerCard(
    namespace: string,
    id: string,
    component: Component<CardProps>,
    options?: CardOptions,
  ): void {
    const declaration = cardsOf(this.#declared[namespace]).find(
      (card) => card.id === id,
    );
    if (declaration === undefined) {
      this.#refusals.push({
        namespace,
        reason: 'undeclared',
        slot: 'dashboard-card',
        name: id,
      });
      throw new UndeclaredRegistration(namespace, 'dashboard-card', id);
    }
    // A type names one component. A second registration is a mistake in the
    // bundle, so it throws, and the loader drops the module whole.
    const type = `${namespace}/${id}`;
    if (this.#cards.some((card) => card.type === type)) {
      throw new Error(`${type} is registered twice`);
    }

    this.#cards.push({
      type,
      namespace,
      declaration,
      component,
      editor: options?.editor,
    });
  }

  /**
   * One of the core's own cards, under the `core` namespace. Nothing installs
   * it, so it is held to the rules a manifest is held to here, and a card
   * that breaks them throws: that is a mistake in the core, not a module to
   * leave out.
   */
  registerCoreCard(
    declaration: CardDeclaration,
    component: Component<CardProps>,
    options?: CardOptions,
  ): void {
    const problem = cardDeclarationProblem(declaration);
    if (problem !== undefined) throw new Error(problem);
    const type = `${CORE_CARD_NAMESPACE}/${declaration.id}`;
    if (this.#coreCards.some((card) => card.type === type)) {
      throw new Error(`${type} is registered twice`);
    }

    this.#coreCards.push({
      type,
      namespace: CORE_CARD_NAMESPACE,
      declaration,
      component,
      editor: options?.editor,
    });
  }

  /** Everything one module contributed, dropped together. */
  forget(namespace: string): void {
    this.#pages = this.#pages.filter((page) => page.namespace !== namespace);
    this.#sections = this.#sections.filter(
      (section) => section.namespace !== namespace,
    );
    this.#actions = this.#actions.filter(
      (action) => action.namespace !== namespace,
    );
    this.#cards = this.#cards.filter((card) => card.namespace !== namespace);
    this.#components = this.#components.filter(
      (held) => held.namespace !== namespace,
    );
  }

  /**
   * Forgets every module: what each one declared, registered and was refused
   * for. The loader calls it when the person changes, so no page, card or
   * pane admitted for one person is drawn for the next.
   */
  clear(): void {
    this.#pages = [];
    this.#sections = [];
    this.#actions = [];
    this.#cards = [];
    this.#refusals = [];
    this.#declared = {};
    this.#hidden = {};
    this.#components = [];
    this.#exports = {};
  }

  /** Records a refusal the loader found, such as a range that does not hold. */
  refuse(refusal: SlotRefusal): void {
    this.#refusals.push(refusal);
  }

  /** Drops the refusals, which a reload does before it admits anything again. */
  clearRefusals(): void {
    this.#refusals = [];
  }

  /**
   * What a person calls a module: the title of the first page its manifest
   * declares, and undefined for a module that declares none.
   */
  moduleTitle(namespace: string): string | undefined {
    return this.#declared[namespace]?.pages?.[0]?.title;
  }

  /** Every registered page, hidden ones too: a hidden page still answers its route. */
  pages(): readonly RegisteredPage[] {
    return this.#pages;
  }

  sections(slot: SectionSlot): readonly RegisteredSection[] {
    return this.#sections.filter(
      (row) =>
        row.slot === slot &&
        !this.#isHidden(row.namespace, SLOT_KEYS[slot], row.id),
    );
  }

  actions(slot: ActionSlot): readonly RegisteredAction[] {
    return this.#actions.filter(
      (row) =>
        row.slot === slot &&
        !this.#isHidden(row.namespace, SLOT_KEYS[slot], row.id),
    );
  }

  /**
   * `#declared`, without a namespace the loader refused: a library mismatch
   * it never loaded for, or a bundle it dropped whole after a throw. Both
   * leave a row in `#refusals`, so a refusal is what a tab strip or a menu
   * checks to stay clear of a module that is not going to run.
   */
  #declaring(): readonly [string, ModuleContributes][] {
    return Object.entries(this.#declared).filter(
      ([namespace]) =>
        !this.#refusals.some((refusal) => refusal.namespace === namespace),
    );
  }

  /** The sections a slot's declaring modules carry, before any bundle registers. */
  declaredSections(slot: SectionSlot): readonly DeclaredEntry[] {
    return this.#declaring().flatMap(([namespace, contributes]) =>
      (contributes[SLOT_KEYS[slot]] ?? [])
        .filter(
          (entry) => !this.#isHidden(namespace, SLOT_KEYS[slot], entry.id),
        )
        .map((entry) => ({
          namespace,
          id: entry.id,
          title: entry.title,
          fileTypes: entry.file_types,
          devices: entry.devices,
          crossOriginIsolated: entry.cross_origin_isolated,
        })),
    );
  }

  /**
   * The namespaces that declared an entry in a section slot and that the
   * loader refused, so a screen can say a module's part is not available
   * rather than leave a gap.
   */
  refusedSections(slot: SectionSlot): readonly DeclaredEntry[] {
    return Object.entries(this.#declared)
      .filter(([namespace]) => this.refused(namespace))
      .flatMap(([namespace, contributes]) =>
        (contributes[SLOT_KEYS[slot]] ?? []).map((entry) => ({
          namespace,
          id: entry.id,
          title: entry.title,
          fileTypes: entry.file_types,
          devices: entry.devices,
          crossOriginIsolated: entry.cross_origin_isolated,
        })),
      );
  }

  /** The actions a slot's declaring modules carry, before any bundle registers. */
  declaredActions(slot: ActionSlot): readonly DeclaredAction[] {
    return this.#declaring().flatMap(([namespace, contributes]) =>
      entriesOf(contributes, SLOT_KEYS[slot])
        .filter(
          (entry) => !this.#isHidden(namespace, SLOT_KEYS[slot], entry.id),
        )
        .map((entry) => ({
          namespace,
          id: entry.id,
          title: entry.title,
          icon: entry.icon as Icon | undefined,
          destructive: entry.destructive ?? false,
          fileTypes: entry.file_types,
          primary: entry.primary ?? false,
          devices: entry.devices,
        })),
    );
  }

  /**
   * Every card a person may place: the core's, then each declaring module's,
   * before any bundle loads. The card picker and the settings form read it.
   */
  declaredCards(): readonly DeclaredCard[] {
    return [
      ...this.#coreCards.map(({ type, namespace, declaration }) => ({
        type,
        namespace,
        declaration,
      })),
      ...this.#declaring().flatMap(([namespace, contributes]) =>
        cardsOf(contributes).map((declaration) => ({
          type: `${namespace}/${declaration.id}`,
          namespace,
          declaration,
        })),
      ),
    ];
  }

  /**
   * The card a dashboard places by `type`, or undefined while its module's
   * bundle has not registered it, or when no module declares it any more.
   * The dashboard draws a placeholder in its cell then.
   */
  card(type: string): RegisteredCard | undefined {
    return (
      this.#coreCards.find((card) => card.type === type) ??
      this.#cards.find((card) => card.type === type)
    );
  }

  refusals(): readonly SlotRefusal[] {
    return this.#refusals;
  }

  /**
   * One sidebar entry per page, unless the manifest hides the page (ADR-20260911-4p6
   * rule 10) or the operator does (ADR-20261009-wax).
   * The href is built here and never by the module: the namespace is the
   * host's, derived from where the module was installed, so a module that
   * could write its own href could address a page it does not own (rule 15).
   *
   * These are the module's items alone. An app puts them after its own with
   * `withModuleNav` beside this file, which is the order rule 9 fixes.
   */
  sidebarItems(): readonly NavItem[] {
    return this.pages()
      .filter(
        (page) =>
          !page.hidden && !this.#isHidden(page.namespace, 'pages', page.path),
      )
      .map((page) => ({
        id: `module:${page.namespace}/${page.path}`,
        label: page.title,
        href: modulePageHref(page.namespace, page.path),
      }));
  }
}
