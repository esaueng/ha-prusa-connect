// Runs one declared entry of an action slot on its subject: waits for the
// module's bundle, finds the run function the declaration promised, and says
// a failure in a toast rather than losing it in the console. A menu item and
// the drop question both run actions this way.
import { t } from '../i18n';
import { toast } from '../primitives';
import type { SlotContext } from './context';
import type { ActionSlot } from './generated';
import type { DeclaredEntry } from './registry.svelte';

/** Runs `entry` on `subject`. It never throws: a failure is a toast. */
export const runSlotAction = async (
  slots: SlotContext | undefined,
  slot: ActionSlot,
  entry: Pick<DeclaredEntry, 'namespace' | 'id' | 'title'>,
  subject: string,
): Promise<void> => {
  try {
    // The bundle may still be loading: wait for it before looking for the
    // run function the declaration promised.
    await slots?.loader?.activate();
    const row = slots?.registry
      .actions(slot)
      .find(
        (action) =>
          action.namespace === entry.namespace && action.id === entry.id,
      );
    if (row === undefined) {
      toast.error(t('slotActions.missing', { module: entry.namespace }));

      return;
    }

    await row.run(subject);
  } catch (thrown) {
    // A synchronous throw and a rejection both land here: `await` turns
    // the former into the latter before `catch` sees it.
    toast.error(entry.title, {
      description: thrown instanceof Error ? thrown.message : String(thrown),
    });
  }
};
