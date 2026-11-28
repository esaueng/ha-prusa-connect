<!--
  The thin line under the 3D view: how many parts there are and which are
  selected, the keys, why the Print button is off, and the printer's bed.
-->
<script lang="ts">
  import { formatNumber } from '@prusa/connect-ui/i18n';

  import type { STLBed, Translate } from '../../api';

  let {
    parts,
    selected,
    selectedName,
    bed,
    printerName,
    blocked,
    blockedId,
    t,
  }: {
    parts: number;
    selected: number;
    /** The name of the one part selected, when one alone is. */
    selectedName: string | undefined;
    bed: STLBed | undefined;
    printerName: string | undefined;
    /** Why the Print button is off, when it is. */
    blocked: string | undefined;
    /** The id the Print button names this sentence by. */
    blockedId: string;
    t: Translate;
  } = $props();
</script>

<div
  class="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 border-t border-border bg-card px-3 py-1.5 font-mono text-xs text-muted-foreground"
  data-part="plate-status"
>
  <span class="text-foreground"
    >{t('slicer.plate.parts', { count: parts })}</span
  >
  {#if selected > 0}
    <span class="truncate text-foreground">
      {selected === 1 && selectedName
        ? selectedName
        : t('slicer.plate.selected', { count: String(selected) })}
    </span>
  {/if}
  <span class="@max-4xl/plate:hidden">{t('slicer.plate.keys')}</span>
  <span
    id={blockedId}
    class="ms-auto truncate text-warning-text"
    role="status"
    aria-live="polite">{blocked ?? ''}</span
  >
  {#if bed}
    <span>
      {[
        printerName,
        t('slicer.plate.bed', {
          width: formatNumber(bed.max_x - bed.min_x),
          depth: formatNumber(bed.max_y - bed.min_y),
        }),
      ]
        .filter(Boolean)
        .join(' · ')}
    </span>
  {/if}
</div>
