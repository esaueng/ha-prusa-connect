// The slots, the registry that holds a module to its manifest, the host
// object a bundle receives, and the loader that admits one (ADR-20260911-4p6 rules 9
// to 11 and 18 to 26).
export {
  CARD_COLUMNS,
  CARD_ROWS,
  cardDeclarationProblem,
  defaultCardSettings,
  isCardType,
  keptNumber,
  readCardDeclaration,
  readCardSettings,
  type WireCard,
} from './card-settings';
export {
  type CardDeclaration,
  type CardEditorProps,
  type CardOptions,
  type CardProps,
  type CardSetting,
  type CardSettings,
  type CardSize,
  CORE_CARD_NAMESPACE,
  type DeclaredCard,
  type RegisteredCard,
  type SettingKind,
  type SettingOption,
} from './card-types';
export { getSlotContext, type SlotContext, setSlotContext } from './context';
export { runSlotAction } from './run-action';
export {
  fileRefOf,
  parseFileRef,
  parsePrinterFileRef,
  printerFileRefOf,
} from './file-ref';
export { matchesFileRef, matchesFileType } from './file-types';
export { FileView, type FileViewProps } from './file-view';
export { ComponentUnavailable, defineModule, noComponents } from './host';
export type {
  ComponentReason,
  ModuleComponents,
  ModuleHost,
  ModuleSetup,
} from './host';
export { ModuleComponent, type ModuleComponentProps } from './module-component';
export { dynamicImport, type ModuleImport } from './import-module';
export {
  catalogueUrl,
  type CatalogueReader,
  readCatalogue,
} from './read-catalogue';
export {
  createModuleStore,
  forgetModuleCaches,
  ModuleStore,
  type ModuleStorage,
  type ModuleStoreOptions,
  type ScopedStore,
  storeKeyPrefix,
} from './module-store.svelte';
export {
  createModuleLoader,
  type LoadedModule,
  ModuleLoader,
  type ModuleLoaderOptions,
} from './loader.svelte';
export { ModulePage, type ModulePageProps } from './module-page';
export {
  type PrintDialogContext,
  printDialogContext,
  setPrintDialogContext,
} from './print-dialog-context';
export { moduleTabId, moduleTabs } from './module-tabs';
export type { DeviceSelector, SlotDevice } from './device-selector';
export {
  DEFAULT_MODULE_ICON,
  moduleIcon,
  type NavGroup,
  type NavItem,
  withModuleNav,
} from './navigation';
export {
  type DeclaredAction,
  type DeclaredEntry,
  describeRefusal,
  type RegisteredAction,
  type RegisteredComponent,
  type RegisteredPage,
  type RegisteredSection,
  type SlotRefusal,
  SlotRegistry,
  UndeclaredRegistration,
  type StoredFileRef,
  type UploadRequest,
} from './registry.svelte';
export { SlotActions, type SlotActionsProps } from './slot-actions';
export { SlotOutlet, type SlotOutletProps } from './slot-outlet';
export {
  type ActionOutletTarget,
  type ActionRuns,
  type ActionSlot,
  buildRegister,
  type Icon,
  MODULE_ICONS,
  type ModuleContributes,
  type ModuleRegister,
  type ModuleUI,
  type RegisterHandlers,
  type Screen,
  type SectionOutletTarget,
  type SectionProps,
  type SectionSlot,
  SLOT_FORMS,
  SLOT_KEYS,
  SLOT_NAMES,
  SLOT_SCREENS,
  type SlotName,
  type SlotsOf,
} from './types';
export { parseVersion, satisfiesRange } from './version-range';
export { modulePageHref } from '../module-href';
export { hiddenEntries, type HidingInstance } from '../module-hidden';
export { LIBRARY_VERSION } from '../version';
