<!--
  The prints from the queue that ended, newest first, with how each ended.
  The queue keeps only the last few, as the module's settings say.

  It is one row under the queue until a person opens it: what waits is what
  the tab is for, and what ended is looked at now and then. The closed row
  says how many ended and how the last one did, which is the question a
  person comes back with after a print.
-->
<script lang="ts">
  import { formatDateTime } from '@prusa/connect-ui/i18n';
  import {
    Badge,
    Button,
    Collapsible,
    CollapsibleContent,
    CollapsibleTrigger,
  } from '@prusa/connect-ui/primitives';

  import type { Entry, Translate } from '../../api';
  import { outcomeBadge, personOf } from '../../queue-text';
  import { EntryThumbnail } from '../entry-thumbnail';

  let {
    entries,
    device,
    t,
    me,
    mayChange,
    busy = false,
    onClear,
    open = $bindable(false),
  }: {
    /** The ended prints, newest first. */
    entries: readonly Entry[];
    /** The printer the queue is of, for the files' pictures. */
    device?: string | undefined;
    t: Translate;
    /** The principal reading the page, so their own name reads "you". */
    me?: string | undefined;
    mayChange: boolean;
    busy?: boolean;
    onClear: () => void;
    /** Whether the list is drawn under its row. */
    open?: boolean;
  } = $props();

  const last = $derived(entries[0]);
  const outcomeOf = (entry: Entry): string =>
    t(`simple-queue.outcome.${entry.outcome ?? 'unknown'}`);
  const endedAt = (entry: Entry): string =>
    formatDateTime(entry.ended_at ?? entry.changed_at);

  // Who put the file on the queue and who started it: the two things the
  // printer's own History tab does not know.
  const people = (entry: Entry): string => {
    const added = personOf(entry.added_by, me);
    const started = personOf(entry.started_by, me);
    const addedBy = added.you
      ? t('simple-queue.history.addedByYou')
      : t('simple-queue.history.addedBy', { name: added.name });
    if (!entry.started_by) {
      return `${addedBy} · ${t('simple-queue.active.startedByQueue')}`;
    }

    return `${addedBy} · ${
      started.you
        ? t('simple-queue.active.startedByYou')
        : t('simple-queue.active.startedBy', { name: started.name })
    }`;
  };

  // The name the file was written under on the printer, when it is not the
  // file's own: the printer already held that name, so the queue numbered the
  // copy. It is the name the printer's History tab and Jobs list the print by.
  const printedAs = (entry: Entry): string | undefined => {
    const landed = entry.landing?.split('/').at(-1) ?? '';

    return landed === '' || landed === entry.name ? undefined : landed;
  };
</script>

<section class="min-w-0" data-part="history-list">
  {#if entries.length === 0}
    <p class="text-sm text-muted-foreground">
      {t('simple-queue.history.empty')}
    </p>
  {:else}
    <Collapsible bind:open>
      <CollapsibleTrigger variant="inline" class="min-w-0">
        <span class="shrink-0 text-foreground"
          >{t('simple-queue.history.title')}</span
        >
        <span class="shrink-0 font-normal text-muted-foreground tabular-nums"
          >{entries.length}</span
        >
        {#if last && !open}
          <span
            class="ms-auto min-w-0 truncate text-xs font-normal text-muted-foreground"
            data-part="history-last"
          >
            {t('simple-queue.history.last', {
              name: last.name,
              outcome: outcomeOf(last),
              when: endedAt(last),
            })}
          </span>
        {/if}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <ul
          class="mt-2 grid min-w-0 grid-cols-1 divide-y divide-border rounded-md border border-border"
        >
          {#each entries as entry (entry.id)}
            {@const landed = printedAs(entry)}
            <li
              class="flex min-w-0 items-start gap-3 p-3 text-sm"
              data-part="history-entry"
            >
              <EntryThumbnail {device} {entry} name={entry.name} />
              <div class="grid min-w-0 flex-1 grid-cols-1 gap-0.5">
                <span class="min-w-0 truncate font-medium" title={entry.path}
                  >{entry.name}</span
                >
                <span class="truncate text-xs text-muted-foreground"
                  >{people(entry)}</span
                >
                {#if landed}
                  <span
                    class="truncate text-xs text-muted-foreground"
                    data-part="printed-as"
                    >{t('simple-queue.history.printedAs', {
                      name: landed,
                    })}</span
                  >
                {/if}
              </div>
              <div class="grid shrink-0 justify-items-end gap-1">
                <Badge variant={outcomeBadge(entry.outcome)}
                  >{outcomeOf(entry)}</Badge
                >
                <span class="text-xs text-muted-foreground tabular-nums"
                  >{endedAt(entry)}</span
                >
              </div>
            </li>
          {/each}
        </ul>
        {#if mayChange}
          <div class="mt-2 flex justify-end">
            <Button
              variant="ghost"
              size="sm"
              onclick={onClear}
              disabled={busy}
              data-part="clear-history"
            >
              {t('simple-queue.action.clearHistory')}
            </Button>
          </div>
        {/if}
      </CollapsibleContent>
    </Collapsible>
  {/if}
</section>
