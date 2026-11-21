// What the views read, held for them from the module's setup.
//
// A module-level holder and not Svelte context: the setup function runs
// before any component of this module is mounted, so there is no component
// for `setContext` to hang on. It is set once, in `index.ts`, and let go when
// the person who admitted the module leaves.
import type { ModuleHost } from '@prusa/connect-ui/slots';

import type { QueueApi } from './api';
import type { Grants } from './grants';
import type { OverviewStore } from './overview-store.svelte';

/** Everything the views read, built from the host and from nothing else. */
export interface Session {
  readonly host: ModuleHost;
  readonly api: QueueApi;
  /** Lets the module start prints on a printer by itself. */
  readonly grants: Grants;
  /** The queues of every printer, read once for every card on the page. */
  readonly overview: OverviewStore;
}

let held: Session | undefined;

/** Called once by the module's setup function. */
export const holdSession = (session: Session): void => {
  const signal = session.host.signal;
  if (signal.aborted) return;
  held = session;
  signal.addEventListener(
    'abort',
    () => {
      session.overview.dispose();
      if (held === session) held = undefined;
    },
    { once: true },
  );
};

/** What the views read. It throws rather than drawing an empty view. */
export const heldSession = (): Session => {
  if (!held) {
    throw new Error('the print queue module drew a view before its setup ran');
  }

  return held;
};
