<!--
  One printer's queue on a time axis: the file that prints, then each waiting
  file in its order, from now to the end of the last one. A red mark is now.

  The axis has one scale, a mark every three hours and 200 pixels apart until
  the person zooms, so the width of a bar means the same length of print on
  every printer, and a long queue is scrolled, not squeezed.

  Every time is the earliest one possible, because a printer takes the next
  file only once somebody said its bed is clear. The line under the bar says
  so in the one case a person can act on: nothing prints, and the plan starts
  when they start it. A file that names no print time is hatched and drawn as
  an hour, so the bar never ends in nothing.
-->
<script lang="ts">
  import { Card, CardContent } from '@prusa/connect-ui/primitives';

  import type { Translate } from '../../api';
  import { clockOf } from '../../clock';
  import { MARK_HOURS, type Plan, STRIPES, zoomed } from '../../timeline';
  import { TimelineLanes } from '../timeline-lanes';
  import { TimelineZoom } from '../timeline-zoom';

  let {
    plan,
    now,
    t,
  }: {
    plan: Plan;
    /** The moment the plan was laid out from, in milliseconds since the epoch. */
    now: number;
    t: Translate;
  } = $props();

  let hours = $state<number>(MARK_HOURS);
  // Counts the presses of Back to now, each of which scrolls the axis home.
  let home = $state(0);
  const done = $derived(clockOf(plan.end, now));
  const unknown = $derived(plan.slots.some((slot) => !slot.known));
  const waiting = $derived(plan.slots.some((slot) => !slot.active));

  const summary = $derived.by(() => {
    if (!plan.firm) return t('simple-queue.timeline.doneAfter', { when: done });

    return plan.running
      ? t('simple-queue.timeline.done', { when: done })
      : t('simple-queue.timeline.doneIfNow', { when: done });
  });
</script>

<Card data-part="queue-timeline">
  <CardContent class="grid min-w-0 grid-cols-1 gap-3">
    <div class="flex min-w-0 flex-wrap items-center justify-between gap-2">
      <h3 class="text-sm font-medium">{t('simple-queue.timeline.title')}</h3>
      <div class="flex min-w-0 items-center gap-3">
        <p
          class="min-w-0 text-xs text-muted-foreground"
          data-part="timeline-end"
        >
          {summary}
        </p>
        <TimelineZoom bind:hours {t} onHome={() => (home += 1)} />
      </div>
    </div>
    <TimelineLanes
      lanes={[{ key: 'queue', slots: plan.slots }]}
      end={plan.end}
      {now}
      {hours}
      {t}
      onZoom={(closer: boolean) => (hours = zoomed(hours, closer))}
      {home}
    />
    <ul
      class="flex min-w-0 flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground"
    >
      {#if plan.running}
        <li class="flex items-center gap-1.5">
          <span class="size-3 bg-primary"></span>{t(
            'simple-queue.timeline.active',
          )}
        </li>
      {/if}
      {#if waiting}
        <li class="flex items-center gap-1.5">
          <span class="size-3 bg-primary/30"></span>{t(
            'simple-queue.timeline.waiting',
          )}
        </li>
      {/if}
      {#if unknown}
        <li class="flex items-center gap-1.5" data-part="timeline-unknown">
          <span class="size-3 bg-muted" style:background-image={STRIPES}
          ></span>{t('simple-queue.timeline.unknown')}
        </li>
      {/if}
    </ul>
  </CardContent>
</Card>
