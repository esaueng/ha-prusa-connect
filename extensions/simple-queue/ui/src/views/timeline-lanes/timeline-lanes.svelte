<!--
  Queues on one time axis, a lane each: the axis with its marks, a bar for
  every file in its lane, and a red mark at now. A printer's Queue tab draws
  its one queue with it, and the Print queue page every printer's, one under
  another on the same axis, so an hour is as wide in every lane.

  The axis has one scale, so a long queue is wider than its card and is
  scrolled sideways. It starts at the mark before now, so now is in sight
  without scrolling. The bars are a picture of what a list says in words, so
  a screen reader is given that list and not the bars.
-->
<script lang="ts">
  import { formatDate, formatDuration } from '@prusa/connect-ui/i18n';

  import type { Translate } from '../../api';
  import { clockOf } from '../../clock';
  import {
    axisOf,
    MARK_WIDTH,
    placeOf,
    type Slot,
    STRIPES,
  } from '../../timeline';

  let {
    lanes,
    end,
    now,
    hours,
    t,
    lane: laneClass = 'mt-2 h-9',
    onZoom,
    home = 0,
  }: {
    lanes: readonly { key: string; slots: readonly Slot[] }[];
    /** When the last lane ends, which sets how long the axis is. */
    end: number;
    /** The moment the plans were laid out from. */
    now: number;
    /** The hours one mark of the axis spans. */
    hours: number;
    t: Translate;
    /** The classes of one lane: its height, and its border for several. */
    lane?: string;
    /**
     * Zooms the axis one step closer or farther, for the mouse wheel turned
     * with Ctrl held and for a pinch on a touchpad, which the browser sends
     * the same way.
     */
    onZoom?: ((closer: boolean) => void) | undefined;
    /** Each new value scrolls the axis back to where it starts, with now in sight. */
    home?: number;
  } = $props();

  let scroller = $state<HTMLElement>();
  $effect(() => {
    if (home > 0) scroller?.scrollTo({ left: 0, behavior: 'smooth' });
  });

  // How far a pinch moves before it zooms one step, so one gesture is one
  // step and not as many as the touchpad sends events. A notch of a mouse
  // wheel moves at least this far, so it is one step too.
  const PINCH_STEP = 40;

  // The wheel is listened to by hand, because only a listener that is not
  // passive may stop the browser zooming the whole page.
  const pinch = (node: HTMLElement): (() => void) => {
    let pending = 0;
    const wheel = (event: WheelEvent): void => {
      if (!event.ctrlKey || onZoom === undefined) return;
      event.preventDefault();
      // Firefox counts a mouse wheel in lines, a few to a notch. A notch is
      // one step whatever it counts in.
      if (event.deltaMode !== WheelEvent.DOM_DELTA_PIXEL) {
        onZoom(event.deltaY < 0);
        pending = 0;
        return;
      }
      pending += event.deltaY;
      if (Math.abs(pending) < PINCH_STEP) return;
      onZoom(pending < 0);
      pending = 0;
    };
    node.addEventListener('wheel', wheel, { passive: false });

    return () => {
      node.removeEventListener('wheel', wheel);
    };
  };

  const axis = $derived(axisOf({ end }, now, hours));
  const nowAt = $derived(placeOf(axis, now));
  const clock = (at: number): string => clockOf(at, now);

  // A mark names its hour, and the mark at midnight names the day that starts
  // there, so a queue running into another day says which.
  const markOf = (at: number): string =>
    new Date(at).getHours() === 0 && new Date(at).getMinutes() === 0
      ? formatDate(at, { weekday: 'short', day: 'numeric', month: 'short' })
      : formatDate(at, { timeStyle: 'short' });

  // What a bar says on hover: the file, when it prints and for how long.
  const hintOf = (slot: Slot): string => {
    const length = slot.known
      ? formatDuration(Math.round((slot.end - slot.start) / 1000))
      : t('simple-queue.timeline.lengthUnknown');

    return `${slot.entry.name}\n${clock(slot.start)} – ${clock(slot.end)} · ${length}`;
  };
</script>

<div
  class="min-w-0 overflow-x-auto pb-2"
  aria-hidden="true"
  data-part="timeline-scroll"
  bind:this={scroller}
  {@attach pinch}
>
  <div class="relative" style:width="{axis.width}px">
    <!-- A short tick stands at every hour between two marks. -->
    <div
      class="relative h-6 border-b border-border"
      style:background-image="repeating-linear-gradient(to right, var(--border)
      0 1px, transparent 1px {MARK_WIDTH / hours}px)"
      style:background-size="100% 6px"
      style:background-position="{axis.hourAt}px bottom"
      style:background-repeat="no-repeat"
    >
      {#each axis.ticks as tick (tick)}
        <span
          class="absolute top-0 h-6 overflow-hidden border-s border-border ps-1 text-xs leading-5 whitespace-nowrap text-muted-foreground tabular-nums"
          style:left="{placeOf(axis, tick)}px"
          style:width="{MARK_WIDTH}px">{markOf(tick)}</span
        >
      {/each}
    </div>
    {#each lanes as lane (lane.key)}
      <div
        class={['relative', laneClass]}
        data-part="timeline-lane"
        data-lane={lane.key}
      >
        {#each lane.slots as slot (slot.entry.id)}
          {@const left = placeOf(axis, slot.start)}
          <div
            class={[
              // The padding is the name's, so a bar is exactly as wide as its
              // print and a short one does not reach over the gap after it.
              'absolute top-1/2 flex h-9 -translate-y-1/2 items-center overflow-hidden text-xs whitespace-nowrap',
              slot.active
                ? 'bg-primary text-primary-foreground'
                : 'bg-primary/30 text-foreground',
              !slot.known && 'bg-muted text-muted-foreground',
            ]}
            style:left="{left}px"
            style:width="{Math.max(placeOf(axis, slot.end) - left - 2, 2)}px"
            style:background-image={slot.known ? undefined : STRIPES}
            title={hintOf(slot)}
            data-part="timeline-slot"
            data-entry={slot.entry.id}
            data-known={slot.known}
          >
            <span class="truncate px-2">{slot.entry.name}</span>
          </div>
        {/each}
      </div>
    {/each}
    <div
      class="absolute top-0 bottom-0 w-0.5 bg-destructive"
      style:left="{nowAt}px"
      data-part="timeline-now"
    ></div>
  </div>
</div>
