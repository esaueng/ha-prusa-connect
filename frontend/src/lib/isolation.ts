// The server sends a printer tab that asks for a cross-origin isolated page
// with both isolation headers, at its own address only. Headers come only with
// a document, so the app opens and leaves such a tab with a full page load.
// Every other page stays an ordinary page, where resources of other origins
// keep loading.
import {
  matchesPattern,
  paramsOf,
  type Router,
} from '@prusa/connect-ui/router';
import { moduleTabId, type SlotRegistry } from '@prusa/connect-ui/slots';

import { PATHS } from './routes';

/** What the browser reports about the document the app runs in. */
export interface DocumentIsolation {
  /** `crossOriginIsolated`: the document is isolated now. */
  isolated: boolean;
  /**
   * The browser can isolate this document: it is a secure context, HTTPS or
   * `localhost`, and the browser supports `crossOriginIsolated`.
   */
  capable: boolean;
  /**
   * The app runs in a frame of another page. A frame is isolated only when
   * the page around it is too, so an isolated tab opens in a new browser tab.
   */
  framed: boolean;
}

/** The parts of a window `browserIsolation` reads. */
interface IsolationScope {
  crossOriginIsolated?: unknown;
  isSecureContext?: unknown;
  top?: unknown;
  self?: unknown;
}

/**
 * The document the app runs in, read from a window. A browser without
 * `crossOriginIsolated` cannot isolate anything, so `capable` is false and
 * the app routes every address itself.
 */
export const browserIsolation = (
  scope: IsolationScope = globalThis,
): DocumentIsolation => ({
  isolated: scope.crossOriginIsolated === true,
  capable:
    scope.isSecureContext === true &&
    typeof scope.crossOriginIsolated === 'boolean',
  framed: scope.top !== scope.self,
});

/**
 * Whether an address is a module's printer tab that asks for an isolated
 * page. A hidden tab, or one of a module the loader refused, is not one. Like
 * the server, it reads no device selector.
 */
export const isolatedTabAt = (
  registry: SlotRegistry,
  path: string,
): boolean => {
  if (!matchesPattern(PATHS.printerDetailTab, path)) return false;
  const { tab } = paramsOf(PATHS.printerDetailTab, path);

  return registry
    .declaredSections('device-tab')
    .some(
      (entry) =>
        entry.crossOriginIsolated === true &&
        moduleTabId(entry.namespace, entry.id) === tab,
    );
};

/**
 * The router's document rule. `document` when the address wants isolation and
 * the document has none, or the reverse. `window` for an isolated tab while
 * the app runs in a frame of another page. Where the browser cannot isolate,
 * the app routes the address itself.
 */
export const documentFor = (
  registry: SlotRegistry,
  path: string,
  isolation: DocumentIsolation,
): 'document' | 'window' | undefined => {
  if (!isolation.capable) return undefined;
  const wanted = isolatedTabAt(registry, path);
  if (wanted && isolation.framed) return 'window';

  return wanted === isolation.isolated ? undefined : 'document';
};

/**
 * Asks the router to hand an isolated tab's address, and an address that
 * leaves one, to the browser. Returns the call that stops it, for an
 * `$effect` to return.
 */
export const isolateModuleTabs = (
  router: Router,
  registry: SlotRegistry,
  read: () => DocumentIsolation = browserIsolation,
): (() => void) =>
  router.loadDocumentFor((path) => documentFor(registry, path, read()));
