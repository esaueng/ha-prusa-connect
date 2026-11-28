<!--
  The button that finishes the plate. It prints on an idle printer, or adds
  the plate to the queue of a busy one, and shows the print time once the
  plate as it is now has been sliced. Its menu slices without printing,
  downloads the G-code or saves the placed STL.
-->
<script lang="ts">
  import {
    ChevronDown,
    Download,
    Layers,
    ListPlus,
    Play,
    Save,
  } from '@lucide/svelte';
  import { formatDuration } from '@prusa/connect-ui/i18n';
  import {
    Button,
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
  } from '@prusa/connect-ui/primitives';

  import type { Translate } from '../../api';
  import type { FlowStep } from '../../plate/plate-flow.svelte';
  import type { PrintDecision } from '../../plate/printers';

  let {
    decision,
    estimate,
    step,
    canSlice,
    canSave,
    describedBy,
    t,
    onPrint,
    onSliceOnly,
    onDownload,
    onSaveStl,
  }: {
    decision: PrintDecision;
    /** The print time of the plate as it is, in seconds, from a finished slice. */
    estimate: number | undefined;
    step: FlowStep;
    /** The plate can be sliced, whatever the printer is doing. */
    canSlice: boolean;
    /** The plate can be saved as an STL. */
    canSave: boolean;
    /** The id of the sentence that says why the button is off. */
    describedBy?: string | undefined;
    t: Translate;
    onPrint: () => void;
    onSliceOnly: () => void;
    onDownload: () => void;
    onSaveStl: () => void;
  } = $props();

  const busy = $derived(step !== 'idle');
  const label = $derived(
    busy
      ? t(`slicer.plate.step.${step}`)
      : t(
          decision.action === 'queue'
            ? 'slicer.plate.queue'
            : 'slicer.plate.print',
        ),
  );
</script>

<div class="flex items-stretch gap-px" data-part="print-button">
  <Button
    size="field"
    pending={busy}
    disabled={decision.block !== undefined}
    aria-describedby={decision.block === undefined ? undefined : describedBy}
    onclick={onPrint}
  >
    {#if !busy}
      {#if decision.action === 'queue'}<ListPlus
          aria-hidden="true"
        />{:else}<Play aria-hidden="true" />{/if}
    {/if}
    {label}
    {#if estimate !== undefined && !busy}
      <span class="font-mono text-xs font-normal">
        <span class="sr-only">{t('slicer.plate.estimate')}</span>
        {formatDuration(estimate)}
      </span>
    {/if}
  </Button>
  <DropdownMenu>
    <DropdownMenuTrigger>
      {#snippet child({ props })}
        <Button {...props} size="icon-lg" label={t('slicer.plate.printOptions')}
          ><ChevronDown /></Button
        >
      {/snippet}
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end">
      <DropdownMenuItem disabled={!canSlice || busy} onSelect={onSliceOnly}>
        <Layers />{t('slicer.plate.sliceOnly')}
      </DropdownMenuItem>
      <DropdownMenuItem disabled={!canSlice || busy} onSelect={onDownload}>
        <Download />{t('slicer.plate.downloadGcode')}
      </DropdownMenuItem>
      <DropdownMenuItem disabled={!canSave || busy} onSelect={onSaveStl}>
        <Save />{t('slicer.plate.saveStl')}
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</div>
