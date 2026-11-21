<!--
  The files on their way to a printer: those this server is sending to the
  printer, then the files this tab is sending behind them, going up to this
  server or waiting their turn, each with its progress and the button that
  stops it. Nothing is
  drawn while nothing moves. The Now tab draws it among the files, and a
  module's tab above its own content, so a file a module sent is seen where
  it was sent from.
-->
<script lang="ts">
  import { formatBytes, formatUnit, t } from '../../../i18n';
  import { Button, ClippedName, Progress } from '../../../primitives';
  import type { SendsIndicatorRow } from '../../sends-indicator';
  import PendingUpload from '../pending-upload.svelte';
  import type { PrinterTransferRow, PrinterTransferSource } from './types';

  let {
    transfers,
    uploads,
    onCancelUpload,
    label,
  }: {
    transfers?: PrinterTransferSource | undefined;
    uploads?: readonly SendsIndicatorRow[] | undefined;
    onCancelUpload?: ((id: string) => void) | undefined;
    /** The classes of the heading, so it reads as the blocks around it. */
    label: string;
  } = $props();

  const PERCENT = 100;

  const moving = $derived(transfers?.inFlight ?? []);

  const nameOf = (transfer: PrinterTransferRow): string =>
    transfer.path.split('/').pop() ?? transfer.path;

  /** Null while the length is unknown, which draws a bar with no fill. */
  const percentOf = (transfer: PrinterTransferRow): number | null =>
    transfer.bytesTotal > 0
      ? (transfer.bytesSent / transfer.bytesTotal) * PERCENT
      : null;
</script>

{#snippet transferRow(transfer: PrinterTransferRow)}
  {@const name = nameOf(transfer)}
  {@const percent = percentOf(transfer)}
  <div class="flex flex-col gap-2 rounded-sm bg-surface px-3 py-2">
    <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
      <ClippedName {name} class="min-w-0 font-mono text-sm" />
      {#if transfer.bytesTotal > 0}
        <span
          class="shrink-0 font-mono text-xs text-muted-foreground tabular-nums"
        >
          {formatBytes(transfer.bytesSent)} / {formatBytes(transfer.bytesTotal)}
        </span>
      {/if}
    </div>
    <div class="flex items-center gap-3">
      <Progress
        value={percent}
        aria-label={t('printerDetailStatus.transfer.label', { name })}
        size="sm"
        class="flex-1"
      />
      {#if percent !== null}
        <span
          class="shrink-0 font-mono text-xs text-muted-foreground tabular-nums"
        >
          {formatUnit(Math.round(percent), '%')}
        </span>
      {/if}
      <Button
        variant="ghost"
        size="sm"
        class="shrink-0"
        aria-label={t('printerDetailStatus.transfer.stopFor', { name })}
        onclick={() => void transfers?.stop(transfer.transferId)}
      >
        {t('printerDetailStatus.transfer.stop')}
      </Button>
    </div>
  </div>
{/snippet}

{#snippet uploadRow(upload: SendsIndicatorRow)}
  <div class="flex flex-col gap-2 rounded-sm bg-surface px-3 py-2">
    <PendingUpload {upload} onCancel={onCancelUpload} />
  </div>
{/snippet}

{#if moving.length > 0 || (uploads?.length ?? 0) > 0}
  <div class="flex flex-col gap-2" data-part="printer-transfers">
    <span class={label}>
      {t('printerDetailStatus.transfer.heading')}
    </span>
    <!-- The file going to the printer first, then the files waiting their
         turn behind it, in the order they go. -->
    {#each moving as transfer (transfer.transferId)}
      {@render transferRow(transfer)}
    {/each}
    {#each uploads ?? [] as upload (upload.id)}
      {@render uploadRow(upload)}
    {/each}
  </div>
{/if}
