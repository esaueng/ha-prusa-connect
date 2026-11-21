<!--
  When a queued file prints, at the right of its row: the earliest start and
  end and how long the print takes. A file that names no print time has a
  start and no end, and behind such a file a start is only a lower bound, so
  it reads "after". The file on the printer says when it ends and how long is
  left. Nothing is drawn for a row the plan has no slot for.
-->
<script lang="ts">
  import { formatDuration } from '@prusa/connect-ui/i18n';

  import type { Translate } from '../../api';
  import { clockOf } from '../../clock';
  import type { Slot } from '../../timeline';

  let {
    slot,
    now,
    t,
    align = 'end',
  }: {
    slot: Slot | undefined;
    /** The moment the plan starts from, so a time on another day names its day. */
    now: number;
    t: Translate;
    /** The side the lines keep to: the right of a row, the left of a card. */
    align?: 'start' | 'end';
  } = $props();

  const SECOND_MS = 1000;
  const clock = (at: number): string => clockOf(at, now);
  const length = (from: number, to: number): string =>
    formatDuration(Math.round((to - from) / SECOND_MS));

  const lines = $derived.by((): readonly [string, string] | undefined => {
    if (slot === undefined) return undefined;

    if (slot.active) {
      return slot.known
        ? [
            t('simple-queue.times.ends', { when: clock(slot.end) }),
            t('simple-queue.times.left', {
              duration: length(slot.start, slot.end),
            }),
          ]
        : undefined;
    }

    const from = clock(slot.start);
    if (!slot.known) {
      return [
        slot.firm
          ? t('simple-queue.times.from', { from })
          : t('simple-queue.times.after', { from }),
        t('simple-queue.times.unknown'),
      ];
    }

    return [
      slot.firm
        ? t('simple-queue.times.range', { from, to: clock(slot.end) })
        : t('simple-queue.times.after', { from }),
      length(slot.start, slot.end),
    ];
  });
</script>

{#if lines}
  <div
    class={[
      'grid shrink-0 gap-0.5 text-xs tabular-nums',
      align === 'end' ? 'justify-items-end' : 'justify-items-start',
    ]}
    data-part="entry-times"
  >
    <span class="font-medium">{lines[0]}</span>
    <span class="text-muted-foreground">{lines[1]}</span>
  </div>
{/if}
