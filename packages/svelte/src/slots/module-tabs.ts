// The tab strip and the outlet read declared entries rather than registered
// ones, because a deep link to a module tab opens before the loader has
// imported anything: the strip must show the tab, and the URL must survive a
// reload, whether or not the bundle behind it has run yet.
import { matchesFileRef } from './file-types';
import type { SlotRegistry } from './registry.svelte';
import { matchesDevice, type SlotDevice } from './device-selector';

/** The tab id a module tab is routed under: `<namespace>.<id>`. */
export const moduleTabId = (namespace: string, id: string): string =>
  `${namespace}.${id}`;

interface ModuleTab {
  id: string;
  label: string;
  /**
   * Whether the module asks for the tab's page to be cross-origin isolated.
   * Where the browser can isolate, opening and leaving such a tab loads a new
   * page.
   */
  isolated: boolean;
}

/**
 * The tabs the manifests declare for a slot, in install order. For
 * `device-tab`, pass the device record: a tab with a devices selector is kept
 * only when the record meets it, and with no record no such tab is kept
 * (ADR-20261110-5np). Pass an object that changes only when `resident`
 * does, because a new record object builds the tab list again. For
 * `file-tab`, pass the file reference: a tab that names file types is kept
 * only for a file of one of them (ADR-20261027-wvj rule 17).
 */
export function moduleTabs(
  registry: SlotRegistry,
  slot: 'device-tab',
  device?: SlotDevice,
): readonly ModuleTab[];
// A file tab takes the file reference here, and so does a caller that holds
// the slot as a union.
export function moduleTabs(
  registry: SlotRegistry,
  slot: 'device-tab' | 'settings' | 'file-tab',
  fileRef?: string,
): readonly ModuleTab[];
export function moduleTabs(
  registry: SlotRegistry,
  slot: 'device-tab' | 'settings' | 'file-tab',
  subject?: SlotDevice | string,
): readonly ModuleTab[] {
  return registry
    .declaredSections(slot)
    .filter((entry) => {
      if (slot === 'device-tab') {
        return matchesDevice(
          entry.devices,
          typeof subject === 'object' ? subject : undefined,
        );
      }

      return (
        typeof subject !== 'string' || matchesFileRef(entry.fileTypes, subject)
      );
    })
    .map((entry) => ({
      id: moduleTabId(entry.namespace, entry.id),
      label: entry.title,
      isolated: entry.crossOriginIsolated === true,
    }));
}
