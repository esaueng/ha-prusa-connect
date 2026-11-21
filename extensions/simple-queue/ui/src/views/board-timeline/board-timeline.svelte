<!--
  Every printer's queue on one time axis, a lane each, as the Print queue
  page shows them. Beside each lane is the printer: its name, which opens its
  Queue tab, its state and how many files wait. The files are listed,
  changed and started on that tab.
-->
<script lang="ts">
  import {
    Badge,
    Card,
    CardContent,
    PrinterIcon,
  } from '@prusa/connect-ui/primitives';

  import type { Queue, Translate } from '../../api';
  import { printerState, queuedOf } from '../../queue-text';
  import { printerModelOf, stateBadgeVariant } from '@prusa/connect-ui/shell';
  import { MARK_HOURS, planOf, STRIPES, zoomed } from '../../timeline';
  import { TimelineLanes } from '../timeline-lanes';
  import { TimelineZoom } from '../timeline-zoom';

  let {
    queues,
    t,
    hrefOf,
    now,
  }: {
    queues: readonly Queue[];
    t: Translate;
    /** The Queue tab of one printer. */
    hrefOf: (device: string) => string;
    /** The moment the plans start from. The clock, unless a test holds it. */
    now?: number | undefined;
  } = $props();

  let hours = $state<number>(MARK_HOURS);
  // Counts the presses of Back to now, each of which scrolls the axis home.
  let home = $state(0);

  // Laid out again each time the queues are read, so the plans move with the
  // clock.
  const laid = $derived.by(() => {
    const at = now ?? Date.now();

    return {
      at,
      rows: queues.map((queue) => ({ queue, plan: planOf(queue, at) })),
    };
  });
  const end = $derived(
    Math.max(laid.at, ...laid.rows.map((row) => row.plan?.end ?? laid.at)),
  );
  const unknown = $derived(
    laid.rows.some((row) => row.plan?.slots.some((slot) => !slot.known)),
  );
</script>

<Card data-part="board-timeline">
  <CardContent class="grid min-w-0 grid-cols-1 gap-3">
    <div class="flex min-w-0 items-center justify-between gap-2">
      <h2 class="text-sm font-medium">{t('simple-queue.timeline.title')}</h2>
      <TimelineZoom bind:hours {t} onHome={() => (home += 1)} />
    </div>
    <div class="flex min-w-0">
      <!-- The printers, one a lane, in line with the lanes beside them. On a
           phone the column keeps the name and the state and leaves the
           picture and the count out, so the lanes keep most of the width. -->
      <ul
        class="w-28 shrink-0 border-e border-border sm:w-72"
        data-part="board-printers"
      >
        <li class="h-6 border-b border-border" aria-hidden="true"></li>
        {#each laid.rows as row (row.queue.device_id)}
          {@const queue = row.queue}
          {@const state = printerState(queue.printer)}
          <li
            class="flex h-16 min-w-0 items-center gap-3 border-b border-border pe-3"
            data-part="board-printer"
            data-device={queue.device_id}
          >
            <PrinterIcon
              model={printerModelOf(queue.printer.model ?? '')}
              reported={queue.printer.model}
              class="hidden size-12 shrink-0 sm:block"
            />
            <div class="grid min-w-0 flex-1 content-center gap-1">
              <a
                href={hrefOf(queue.device_id)}
                class="min-w-0 truncate rounded-sm text-sm font-medium underline underline-offset-4 outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >{queue.printer.name}</a
              >
              <div class="flex min-w-0 items-center gap-2">
                <Badge variant={stateBadgeVariant(state)}
                  >{t(`simple-queue.state.${state}`)}</Badge
                >
                <span
                  class="hidden min-w-0 truncate text-xs text-muted-foreground sm:inline"
                  >{t('simple-queue.board.waiting', {
                    count: queuedOf(queue),
                  })}</span
                >
              </div>
            </div>
          </li>
        {/each}
      </ul>
      <div class="min-w-0 flex-1">
        <TimelineLanes
          lanes={laid.rows.map((row) => ({
            key: row.queue.device_id,
            slots: row.plan?.slots ?? [],
          }))}
          {end}
          now={laid.at}
          {hours}
          {t}
          onZoom={(closer: boolean) => (hours = zoomed(hours, closer))}
          {home}
          lane="h-16 border-b border-border"
        />
      </div>
    </div>
    {#if unknown}
      <p
        class="flex items-center gap-1.5 text-xs text-muted-foreground"
        data-part="timeline-unknown"
      >
        <span class="size-3 bg-muted" style:background-image={STRIPES}
        ></span>{t('simple-queue.timeline.unknown')}
      </p>
    {/if}
  </CardContent>
</Card>
