<!--
  Every printer's queue as a row of cards, the other way the Print queue page
  shows them. Beside each row is the printer, as on the timeline. A person
  who may change a queue drags its waiting cards to another place in their
  row. Each card opens the printer's Queue tab, where the rest is changed.
-->
<script lang="ts">
  import { Badge, PrinterIcon } from '@prusa/connect-ui/primitives';
  import { printerModelOf, stateBadgeVariant } from '@prusa/connect-ui/shell';

  import type { Queue, Translate } from '../../api';
  import { printerState, queuedOf } from '../../queue-text';
  import { planOf, slotsOf } from '../../timeline';
  import { QueueCards } from '../queue-cards';

  let {
    queues,
    t,
    hrefOf,
    now,
    onMove,
  }: {
    queues: readonly Queue[];
    t: Translate;
    /** The Queue tab of one printer. */
    hrefOf: (device: string) => string;
    /** The moment the plans start from. The clock, unless a test holds it. */
    now?: number | undefined;
    /** Moves a waiting file; position counts the waiting files from 0. */
    onMove?:
      ((device: string, entry: string, position: number) => void) | undefined;
  } = $props();

  const laid = $derived.by(() => {
    const at = now ?? Date.now();

    return {
      at,
      rows: queues.map((queue) => ({
        queue,
        slots: slotsOf(planOf(queue, at)),
      })),
    };
  });
</script>

<div class="grid min-w-0 grid-cols-1 gap-3" data-part="board-cards">
  {#each laid.rows as row (row.queue.device_id)}
    {@const queue = row.queue}
    {@const state = printerState(queue.printer)}
    <section
      class="flex min-w-0 flex-col gap-3 rounded-md border border-border bg-card p-3 md:flex-row"
      data-part="board-card-row"
      data-device={queue.device_id}
      aria-label={queue.printer.name}
    >
      <div
        class="flex min-w-0 items-center gap-3 md:w-56 md:shrink-0 md:items-start"
      >
        <PrinterIcon
          model={printerModelOf(queue.printer.model ?? '')}
          reported={queue.printer.model}
          class="size-10 shrink-0 sm:size-12"
        />
        <div class="grid min-w-0 gap-1">
          <a
            href={hrefOf(queue.device_id)}
            class="min-w-0 truncate rounded-sm text-sm font-medium underline underline-offset-4 outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >{queue.printer.name}</a
          >
          <!-- The state and the count share a line, which saves a line of
               height for each printer. -->
          <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <Badge variant={stateBadgeVariant(state)} class="w-fit"
              >{t(`simple-queue.state.${state}`)}</Badge
            >
            <span class="text-xs text-muted-foreground"
              >{t('simple-queue.board.waiting', {
                count: queuedOf(queue),
              })}</span
            >
          </div>
        </div>
      </div>
      <QueueCards
        {queue}
        slots={row.slots}
        now={laid.at}
        {t}
        href={hrefOf(queue.device_id)}
        onMove={onMove === undefined
          ? undefined
          : (entry: string, position: number) => {
              onMove(queue.device_id, entry, position);
            }}
      />
    </section>
  {/each}
</div>
