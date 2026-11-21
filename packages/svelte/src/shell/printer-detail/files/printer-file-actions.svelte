<script lang="ts">
  import HardDriveDownloadIcon from '@lucide/svelte/icons/hard-drive-download';
  import PlayIcon from '@lucide/svelte/icons/play';
  import Trash2Icon from '@lucide/svelte/icons/trash-2';

  import { t } from '../../../i18n';
  import {
    ABOVE_ROW_LINK,
    DropdownMenuItem,
    RowActions,
  } from '../../../primitives';
  import type { PrinterFileEntry, PrinterFileRowContext } from './types';

  let {
    entry,
    row,
  }: {
    entry: PrinterFileEntry;
    row: PrinterFileRowContext;
  } = $props();

  const print = $derived(row.printControl(entry));
  const offers = $derived(
    print !== undefined || row.canFlash(entry) || row.canDelete(entry),
  );
  // A folder gets no module entries, so its menu stays the panel's own.
  const hosted = $derived(!entry.folder && row.fileActions !== undefined);
</script>

{#snippet items()}
  {#if print}
    <DropdownMenuItem
      disabled={print.disabled}
      onSelect={() => row.onPrint(entry)}
    >
      <PlayIcon aria-hidden="true" />
      <!-- The menu is the one place on this row with the width for the. -->
      <span class="flex max-w-64 flex-col">
        <span>{t('printerDetailFiles.action.print')}</span>
        {#if print.reason}
          <span class="text-xs text-muted-foreground">{print.reason}</span>
        {/if}
      </span>
    </DropdownMenuItem>
  {/if}
  {#if row.canFlash(entry)}
    <DropdownMenuItem
      onSelect={() => {
        row.onFlash(entry);
      }}
    >
      <HardDriveDownloadIcon aria-hidden="true" />
      {t('printerDetail.files.action.flash')}
    </DropdownMenuItem>
  {/if}
  {#if row.canDelete(entry)}
    <DropdownMenuItem
      variant="destructive"
      onSelect={() => {
        row.onDelete(entry);
      }}
    >
      <Trash2Icon aria-hidden="true" />
      {t('printerDetailFiles.action.delete')}
    </DropdownMenuItem>
  {/if}
{/snippet}

{#if hosted}
  {@render row.fileActions?.(entry, offers ? items : undefined)}
{:else if offers}
  <RowActions name={row.nameOf(entry)} class={ABOVE_ROW_LINK}>
    {@render items()}
  </RowActions>
{/if}
