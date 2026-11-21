<!--
  The zoom of a time axis: closer draws an hour wider, so a short print's bar
  is wide enough to read its name, and farther fits more days in sight. Back
  to now puts the zoom as it was and the axis where it starts, with now in
  sight.
-->
<script lang="ts">
  import { Button } from '@prusa/connect-ui/primitives';
  import ZoomInIcon from '@lucide/svelte/icons/zoom-in';
  import ZoomOutIcon from '@lucide/svelte/icons/zoom-out';

  import type { Translate } from '../../api';
  import { MARK_HOURS, ZOOMS, zoomed } from '../../timeline';

  let {
    hours = $bindable(MARK_HOURS),
    t,
    onHome,
  }: {
    /** The hours one mark of the axis spans, one of ZOOMS. */
    hours?: number;
    t: Translate;
    /** Brings the axis back to where it starts. */
    onHome?: (() => void) | undefined;
  } = $props();

  const at = $derived(ZOOMS.findIndex((one) => one === hours));
</script>

<div class="flex items-center gap-0.5" data-part="timeline-zoom">
  <Button
    variant="ghost"
    size="sm"
    onclick={() => {
      hours = MARK_HOURS;
      onHome?.();
    }}
    data-part="timeline-home"
  >
    {t('simple-queue.timeline.home')}
  </Button>
  <Button
    variant="ghost"
    size="icon-sm"
    label={t('simple-queue.timeline.zoomOut')}
    disabled={at >= ZOOMS.length - 1}
    onclick={() => {
      hours = zoomed(hours, false);
    }}
  >
    <ZoomOutIcon aria-hidden="true" class="size-4" />
  </Button>
  <Button
    variant="ghost"
    size="icon-sm"
    label={t('simple-queue.timeline.zoomIn')}
    disabled={at <= 0}
    onclick={() => {
      hours = zoomed(hours, true);
    }}
  >
    <ZoomInIcon aria-hidden="true" class="size-4" />
  </Button>
</div>
