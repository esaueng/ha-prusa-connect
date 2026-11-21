<!--
  The files that wait, in the order they print. A person who may print on the
  printer drags a file to another place with the mouse, or moves it up or down
  one place with the arrows, which a finger and the keyboard reach too, or
  takes it off the queue. A file that did not start carries the reason under
  its name.
-->
<script lang="ts">
  import type { Snippet } from 'svelte';

  import { formatDateTime } from '@prusa/connect-ui/i18n';
  import {
    Button,
    EmptyState,
    SelectAllCheckbox,
    SelectCheckbox,
    Selection,
  } from '@prusa/connect-ui/primitives';
  import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
  import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
  import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
  import GripVerticalIcon from '@lucide/svelte/icons/grip-vertical';
  import CalendarClockIcon from '@lucide/svelte/icons/calendar-clock';
  import ListPlusIcon from '@lucide/svelte/icons/list-plus';
  import TrashIcon from '@lucide/svelte/icons/trash-2';

  import type { Entry, Translate } from '../../api';
  import { personOf } from '../../queue-text';
  import type { Slot } from '../../timeline';
  import { EntryThumbnail } from '../entry-thumbnail';
  import { EntryFacts } from '../entry-facts';
  import { EntryTimes } from '../entry-times';

  let {
    entries,
    device,
    t,
    me,
    mayChange,
    limit,
    busy = false,
    onMove,
    onRemove,
    onRemoveMany,
    onAddFiles,
    slots,
    now = Date.now(),
    lead,
  }: {
    /** The waiting files, the next one first. */
    entries: readonly Entry[];
    /** The printer the queue is of, for the files' pictures. */
    device?: string | undefined;
    t: Translate;
    me: string | undefined;
    mayChange: boolean;
    /** How many files may wait on one printer. */
    limit: number;
    busy?: boolean;
    /** position counts the waiting files from 0, the next one. */
    onMove: (entry: string, position: number) => void;
    onRemove: (entry: string) => void;
    /** Takes the ticked files off the queue. Absent offers no ticks. */
    onRemoveMany?: ((entries: string[]) => Promise<void>) | undefined;
    /** Opens the Add files dialog. Absent for a person who may not change the queue. */
    onAddFiles?: (() => void) | undefined;
    /** When each file prints, by its id, from the queue's plan. */
    slots?: ReadonlyMap<string, Slot> | undefined;
    /** The moment the plan starts from. */
    now?: number | undefined;
    /**
     * The file on the printer, drawn as the first row of the list. With it
     * the list is the whole queue, and its heading says so.
     */
    lead?: Snippet | undefined;
  } = $props();

  const uid = $props.id();

  // The files ticked for taking off the queue together. A file that left
  // the queue meanwhile is never acted on.
  const selection = new Selection<string>({
    rows: () => entries.map((entry) => entry.id),
  });
  const ticks = $derived(mayChange && onRemoveMany !== undefined);
  let removing = $state(false);
  const removeTicked = async (): Promise<void> => {
    if (!onRemoveMany) return;
    removing = true;
    try {
      await onRemoveMany([...selection.keys]);
      selection.clear();
    } finally {
      removing = false;
    }
  };

  // Dragging a file: which one, by its id, and where it would land, before or
  // after the row under the pointer. The id and not the row: the list may be
  // read again during a drag, and a row above may come or go.
  let dragged = $state<string | undefined>(undefined);
  let dropAt = $state<{ index: number; before: boolean } | undefined>(
    undefined,
  );
  // The grip stays while a move is saved, so the rows keep their place; only
  // the dragging waits for it.
  const movable = $derived(mayChange && entries.length > 1);
  const draggable = $derived(movable && !busy);

  const overRow = (event: DragEvent, index: number): void => {
    if (dragged === undefined) return;
    event.preventDefault();
    const row = (event.currentTarget as HTMLElement).getBoundingClientRect();
    dropAt = { index, before: event.clientY < row.top + row.height / 2 };
  };

  // The place a file lands at counts the waiting files without it.
  const dropRow = (event: DragEvent): void => {
    event.preventDefault();
    const id = dragged;
    const at = dropAt;
    dragged = undefined;
    dropAt = undefined;
    const from = entries.findIndex((entry) => entry.id === id);
    if (from < 0 || at === undefined) return;
    let position = at.before ? at.index : at.index + 1;
    if (from < position) position -= 1;
    if (id !== undefined && position !== from) onMove(id, position);
  };

  const addedBy = (entry: Entry): string => {
    const who = personOf(entry.added_by, me);
    const when = formatDateTime(entry.added_at);

    return who.you
      ? t('simple-queue.facts.addedByYou', { when })
      : t('simple-queue.facts.addedBy', { name: who.name, when });
  };
</script>

{#snippet addAction()}
  <Button onclick={onAddFiles} disabled={busy} data-part="add-files">
    <ListPlusIcon aria-hidden="true" class="size-4" />
    {t('simple-queue.addFiles.open')}
  </Button>
{/snippet}

<section
  class="grid min-w-0 grid-cols-1 gap-2"
  aria-labelledby="{uid}-waiting"
  data-part="waiting-list"
>
  <!-- The header holds the box that ticks every waiting file. With a file
       ticked the title says how many, and what to do with them follows it,
       beside the box, as a printer's Files tab does, so nothing moves. -->
  <!-- On a phone the limit is left out, so Add files stays on the title's
       line, at its end. -->
  <div
    class="flex min-h-control-height flex-wrap items-center justify-between gap-2"
  >
    <!-- In line with the boxes of the rows, which sit inside the list's
         border and padding. -->
    <div
      class={[
        'flex flex-wrap items-center gap-3',
        ticks && entries.length > 0 && 'ps-3',
      ]}
    >
      {#if ticks && entries.length > 0}
        <SelectAllCheckbox
          {selection}
          keys={entries.map((entry) => entry.id)}
          label={t('simple-queue.bulk.all')}
        />
      {/if}
      <h3
        id="{uid}-waiting"
        class="shrink-0 text-sm font-medium whitespace-nowrap"
      >
        {#if ticks && selection.count > 0}
          {t('simple-queue.bulk.selected', { count: selection.count })}
        {:else if lead}
          {t('simple-queue.queue.title')}
          <span class="font-normal text-muted-foreground tabular-nums"
            >{t('simple-queue.queue.counts', {
              active: 1,
              waiting: entries.length,
            })}</span
          >
        {:else}
          {t('simple-queue.waiting.title')}
          <span class="text-muted-foreground tabular-nums"
            >{entries.length}</span
          >
        {/if}
      </h3>
      {#if ticks && selection.count > 0}
        <div class="flex flex-wrap items-center gap-2" data-part="bulk-actions">
          <Button
            variant="ghost"
            size="sm"
            onclick={() => {
              selection.clear();
            }}
          >
            {t('simple-queue.bulk.clear')}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            disabled={busy || removing}
            aria-busy={removing}
            onclick={() => void removeTicked()}
            data-part="remove-ticked"
          >
            <TrashIcon aria-hidden="true" class="size-4" />
            {t('simple-queue.bulk.remove', { count: selection.count })}
          </Button>
        </div>
      {/if}
    </div>
    <!-- Gone while files are ticked, which gives their actions the row. -->
    {#if selection.count === 0}
      <div class="flex items-center gap-3">
        <span class="hidden text-xs text-muted-foreground sm:inline">
          {t('simple-queue.waiting.limit', { max: limit })}
        </span>
        {#if onAddFiles && entries.length > 0}
          <Button
            size="sm"
            disabled={busy || entries.length >= limit}
            onclick={onAddFiles}
            data-part="add-files"
          >
            <ListPlusIcon aria-hidden="true" class="size-4" />
            {t('simple-queue.addFiles.open')}
          </Button>
        {/if}
      </div>
    {/if}
  </div>
  {#if entries.length === 0 && lead}
    <!-- Nothing waits behind the file on the printer, which is still a queue
         of one and not an empty one. -->
    <div class="min-w-0 overflow-hidden rounded-md border border-border">
      {@render lead()}
    </div>
  {:else if entries.length === 0}
    <EmptyState
      icon={CalendarClockIcon}
      heading={t('simple-queue.waiting.empty.title')}
      headingLevel="h4"
      description={onAddFiles
        ? t('simple-queue.waiting.empty.textAdd')
        : t('simple-queue.waiting.empty.text')}
      class="rounded-md border border-dashed border-border py-8"
      {...onAddFiles ? { action: addAction } : {}}
    />
  {:else}
    <div class="min-w-0 overflow-hidden rounded-md border border-border">
      <ol class="grid min-w-0 grid-cols-1 divide-y divide-border">
        {#if lead}
          <li class="min-w-0">{@render lead()}</li>
        {/if}
        {#each entries as entry, index (entry.id)}
          <li
            class={[
              'relative flex min-w-0 flex-wrap items-start gap-3 p-3',
              dragged === entry.id && 'opacity-50',
              // A line laid over the row and not a border marks where the
              // file lands, so the rows do not move while the pointer passes.
              dropAt?.index === index &&
                (dropAt.before
                  ? 'before:absolute before:inset-x-0 before:top-0 before:h-0.5 before:bg-primary'
                  : 'after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:bg-primary'),
            ]}
            data-part="waiting-entry"
            data-entry={entry.id}
            {draggable}
            ondragstart={(event) => {
              dragged = entry.id;
              event.dataTransfer?.setData('text/plain', entry.id);
              if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
            }}
            ondragover={(event) => {
              overRow(event, index);
            }}
            ondrop={dropRow}
            ondragend={() => {
              dragged = undefined;
              dropAt = undefined;
            }}
          >
            {#if movable}
              <!-- The grip is for a mouse, and a narrow screen keeps its room
                   for the file's name: there the arrows move a file. -->
              <span
                class={[
                  'mt-1 hidden shrink-0 text-muted-foreground sm:block',
                  draggable ? 'cursor-grab' : 'opacity-50',
                ]}
                title={t('simple-queue.action.drag')}
                data-part="drag-handle"
              >
                <GripVerticalIcon aria-hidden="true" class="size-4" />
              </span>
            {/if}
            {#if ticks}
              <SelectCheckbox
                class="mt-0.5"
                {selection}
                key={entry.id}
                label={t('simple-queue.bulk.pick', { name: entry.name })}
              />
            {/if}
            <span
              class="flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-medium tabular-nums"
              aria-label={t('simple-queue.entry.number', {
                position: index + 1,
              })}>{index + 1}</span
            >
            <EntryThumbnail {device} {entry} name={entry.name} />
            <!-- The name and the facts keep room for themselves: on a narrow
                 screen the times and the buttons go to a line of their own
                 under them rather than squeeze them away. -->
            <div class="grid min-w-0 flex-1 basis-32 grid-cols-1 gap-0.5">
              <span
                class="min-w-0 truncate text-sm font-medium"
                title={entry.path}>{entry.name}</span
              >
              <EntryFacts {entry} addedBy={addedBy(entry)} {t} />
              {#if entry.note}
                <p
                  class="mt-1 flex items-start gap-1.5 text-xs"
                  data-part="entry-note"
                >
                  <CircleAlertIcon
                    aria-hidden="true"
                    class="mt-px size-3.5 shrink-0 text-warning"
                  />
                  <span class="min-w-0">
                    {t(`simple-queue.note.${entry.note}`)}
                    {#if entry.detail}
                      <span class="text-muted-foreground">({entry.detail})</span
                      >
                    {/if}
                  </span>
                </p>
              {/if}
            </div>
            <div class="ms-auto flex shrink-0 items-start gap-3">
              <EntryTimes slot={slots?.get(entry.id)} {now} {t} />
              {#if mayChange}
                <div class="flex shrink-0 items-center gap-0.5">
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    label={t('simple-queue.action.up', { name: entry.name })}
                    disabled={busy || index === 0}
                    onclick={() => onMove(entry.id, index - 1)}
                    data-part="move-up"
                  >
                    <ArrowUpIcon aria-hidden="true" class="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    label={t('simple-queue.action.down', { name: entry.name })}
                    disabled={busy || index === entries.length - 1}
                    onclick={() => onMove(entry.id, index + 1)}
                    data-part="move-down"
                  >
                    <ArrowDownIcon aria-hidden="true" class="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    label={t('simple-queue.action.remove', {
                      name: entry.name,
                    })}
                    disabled={busy}
                    onclick={() => onRemove(entry.id)}
                    data-part="remove"
                  >
                    <TrashIcon aria-hidden="true" class="size-4" />
                  </Button>
                </div>
              {/if}
            </div>
          </li>
        {/each}
      </ol>
    </div>
  {/if}
</section>
