<!--
  One file this tab is sending to the printer, before the transfer that moves
  it is listed: going up to the server, waiting for the printer, waiting for
  the person's answer, or on its way. The Now tab and the Files tab both draw
  it, each inside its own row.

  The bar is named by the file and described by the step, so a file that
  waits is not read out as uploading.

  The second line is as tall as the row of a transfer with its Stop button,
  so a file turning into its transfer does not move the page.
-->
<script lang="ts">
  import { formatUnit, t } from '../../i18n';
  import { Button, ClippedName, Progress } from '../../primitives';
  import type { SendsIndicatorRow } from '../sends-indicator';

  let {
    upload,
    onCancel,
  }: {
    upload: SendsIndicatorRow;
    /** Cancels the file, by the row's id. */
    onCancel?: ((id: string) => void) | undefined;
  } = $props();

  const PERCENT = 100;
  const STEP: Record<SendsIndicatorRow['step'], string> = {
    uploading: 'printerDetail.pendingUpload.uploading',
    waiting: 'printerDetail.pendingUpload.waiting',
    asking: 'printerDetail.pendingUpload.asking',
    checking: 'printerDetail.pendingUpload.checking',
    replacing: 'printerDetail.pendingUpload.replacing',
    sending: 'printerDetail.pendingUpload.sending',
  };

  const step = $props.id();
  const percent = $derived(
    upload.progress === null ? null : upload.progress * PERCENT,
  );
</script>

<div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
  <ClippedName name={upload.name} class="min-w-0 font-mono text-sm" />
  <span id={step} class="shrink-0 text-xs text-muted-foreground">
    {t(STEP[upload.step])}
  </span>
</div>
<div
  class="flex min-h-control-height items-center gap-3 @max-3xl/app-shell:min-h-touch-target pointer-coarse:min-h-touch-target"
>
  <Progress
    value={percent}
    aria-label={upload.name}
    aria-describedby={step}
    size="sm"
    class="flex-1"
  />
  {#if percent !== null}
    <span class="shrink-0 font-mono text-xs text-muted-foreground tabular-nums">
      {formatUnit(Math.round(percent), '%')}
    </span>
  {/if}
  {#if upload.canCancel}
    <Button
      variant="ghost"
      size="sm"
      class="shrink-0"
      aria-label={t('printerDetail.pendingUpload.cancelFor', {
        name: upload.name,
      })}
      onclick={() => onCancel?.(upload.id)}
    >
      {t('printerDetail.pendingUpload.cancel')}
    </Button>
  {/if}
</div>
