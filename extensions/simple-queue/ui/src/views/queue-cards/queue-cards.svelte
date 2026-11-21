<!--
  One printer's queue as a row of cards. The first card is what the printer
  works on now, kept in place while the row scrolls; after a divider come
  the waiting files in the order they print. A person who may change the
  queue drags a waiting card to another place in the row.
-->
<script lang="ts">
  import { Progress, Tile } from '@prusa/connect-ui/primitives';
  import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';

  import type { Entry, Queue, Translate } from '../../api';
  import { activeOf, waitingOf } from '../../queue-text';
  import type { Slot } from '../../timeline';
  import { EntryThumbnail } from '../entry-thumbnail';
  import { EntryTimes } from '../entry-times';

  let {
    queue,
    slots,
    now,
    t,
    href,
    onMove,
  }: {
    queue: Queue;
    /** When each file prints, by its id, from the queue's plan. */
    slots: ReadonlyMap<string, Slot>;
    /** The moment the plan starts from. */
    now: number;
    t: Translate;
    /** Where a card leads. Without it a card leads nowhere. */
    href?: string | undefined;
    /** Moves a waiting file; position counts the waiting files from 0. */
    onMove?: ((entry: string, position: number) => void) | undefined;
  } = $props();

  const active = $derived(activeOf(queue));
  const waiting = $derived(waitingOf(queue));
  const draggable = $derived(
    onMove !== undefined && queue.may_change && waiting.length > 1,
  );

  // Dragging a waiting card: which one, by its id, and where it would land,
  // before or after the card under the pointer. The id and not the place: the
  // row may be read again during a drag.
  let dragged = $state<string | undefined>(undefined);
  let dropAt = $state<{ index: number; before: boolean } | undefined>(
    undefined,
  );

  const overCard = (event: DragEvent, index: number): void => {
    if (dragged === undefined) return;
    event.preventDefault();
    const card = (event.currentTarget as HTMLElement).getBoundingClientRect();
    dropAt = { index, before: event.clientX < card.left + card.width / 2 };
  };

  const endDrag = (): void => {
    dragged = undefined;
    dropAt = undefined;
  };

  // The place a file lands at counts the waiting files without it.
  const dropCard = (event: DragEvent): void => {
    event.preventDefault();
    const id = dragged;
    const at = dropAt;
    endDrag();
    const from = waiting.findIndex((entry) => entry.id === id);
    if (from < 0 || at === undefined) return;
    let position = at.before ? at.index : at.index + 1;
    if (from < position) position -= 1;
    if (id !== undefined && position !== from) onMove?.(id, position);
  };
</script>

{#snippet tile(entry: Entry, current: boolean)}
  <Tile
    size="sm"
    {href}
    label={href === undefined ? undefined : entry.name}
    class={['w-44 bg-card sm:w-40', current && 'border-primary']}
  >
    <!-- On a phone the picture is small beside the name, so a card is half
         as tall and more printers fit on the screen. -->
    <div class="flex min-w-0 items-start gap-2 sm:grid">
      <EntryThumbnail
        device={queue.device_id}
        {entry}
        name={entry.name}
        class="size-12 sm:aspect-square sm:size-auto! sm:w-full!"
      />
      <div class="grid min-w-0 flex-1 gap-2">
        <!-- A long name keeps two lines; the whole name is in its tooltip. -->
        <span
          class="line-clamp-2 min-w-0 text-sm font-medium wrap-anywhere"
          title={entry.name}>{entry.name}</span
        >
        <EntryTimes slot={slots.get(entry.id)} {now} {t} align="start" />
        {#if current && entry.state === 'printing' && queue.progress !== undefined}
          <Progress
            value={queue.progress}
            max={100}
            class="h-1.5"
            aria-label={t('simple-queue.active.progress', {
              percent: Math.round(queue.progress),
            })}
          />
        {/if}
        {#if entry.note}
          <p class="flex items-start gap-1.5 text-xs" data-part="entry-note">
            <CircleAlertIcon
              aria-hidden="true"
              class="mt-px size-3.5 shrink-0 text-warning"
            />
            <span class="min-w-0">{t(`simple-queue.note.${entry.note}`)}</span>
          </p>
        {/if}
      </div>
    </div>
  </Tile>
{/snippet}

<ul
  class="flex min-w-0 flex-1 items-stretch gap-2 overflow-x-auto pb-3"
  data-part="queue-cards"
>
  <!-- The now slot stays in place while the waiting files scroll. -->
  <li
    class="sticky start-0 z-10 flex shrink-0 items-stretch gap-3 bg-card pe-1"
    data-part="board-now"
  >
    {#if active}
      <div
        class="flex rounded-md border-2 border-transparent"
        data-part="board-card"
        data-entry={active.id}
      >
        {@render tile(active, true)}
      </div>
    {:else}
      <div
        class="flex w-24 items-center justify-center rounded-md border border-dashed border-border p-2 text-center text-xs text-muted-foreground sm:w-40 sm:p-3 sm:text-sm"
        data-part="board-now-empty"
      >
        {t('simple-queue.cards.nothingNow')}
      </div>
    {/if}
    <span
      class="w-1 shrink-0 self-stretch rounded-full bg-foreground/30"
      aria-hidden="true"
    ></span>
  </li>
  {#each waiting as entry, index (entry.id)}
    <li
      class={[
        'flex shrink-0 rounded-md border-2 border-transparent',
        draggable && 'cursor-grab',
        dragged === entry.id && 'opacity-50',
        dropAt?.index === index &&
          (dropAt.before ? 'border-s-primary' : 'border-e-primary'),
      ]}
      data-part="board-card"
      data-entry={entry.id}
      title={draggable ? t('simple-queue.action.drag') : undefined}
      {draggable}
      ondragstart={draggable
        ? (event) => {
            dragged = entry.id;
            event.dataTransfer?.setData('text/plain', entry.id);
            if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move';
          }
        : undefined}
      ondragover={(event) => {
        overCard(event, index);
      }}
      ondrop={dropCard}
      ondragend={endDrag}
    >
      {@render tile(entry, false)}
    </li>
  {:else}
    <li
      class="flex items-center px-2 text-sm text-muted-foreground"
      data-part="board-cards-empty"
    >
      {t('simple-queue.board.waiting', { count: 0 })}
    </li>
  {/each}
</ul>
