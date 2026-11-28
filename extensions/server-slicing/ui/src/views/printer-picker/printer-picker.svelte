<!--
  The printer the plate is for: its name, its state as the printer page tags
  it, its model and the material loaded. The menu lists every printer the
  person may see, with its print volume, the ones that can print now first.
-->
<script lang="ts">
  import { ChevronDown } from '@lucide/svelte';
  import { formatNumber } from '@prusa/connect-ui/i18n';
  import {
    Badge,
    Button,
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuLabel,
    DropdownMenuRadioGroup,
    DropdownMenuRadioItem,
    DropdownMenuTrigger,
  } from '@prusa/connect-ui/primitives';

  import type { Translate } from '../../api';
  import type { PrinterChoice } from '../../plate/printers';
  import { stateBadge } from './variants';

  let {
    printers,
    chosen,
    t,
    onChoose,
  }: {
    printers: readonly PrinterChoice[];
    chosen: string | undefined;
    t: Translate;
    onChoose: (id: string) => void;
  } = $props();

  const current = $derived(printers.find((one) => one.id === chosen));

  const lineOf = (printer: PrinterChoice): string =>
    [printer.model, printer.material].filter(Boolean).join(' · ');

  const bedOf = (printer: PrinterChoice): string =>
    printer.bed
      ? t('slicer.plate.bed', {
          width: formatNumber(printer.bed.max_x - printer.bed.min_x),
          depth: formatNumber(printer.bed.max_y - printer.bed.min_y),
        })
      : '';
</script>

{#snippet tag(printer: PrinterChoice)}
  <Badge variant={stateBadge(printer.state)} size="sm">
    <span>{t(printer.stateKey)}</span>
    {#if printer.progress !== undefined}
      <span>{formatNumber(printer.progress / 100, { style: 'percent' })}</span>
    {/if}
  </Badge>
{/snippet}

<DropdownMenu>
  <DropdownMenuTrigger>
    {#snippet child({ props })}
      <Button
        {...props}
        variant="ghost"
        size="md"
        class="min-w-0"
        data-part="printer-picker"
      >
        <span class="sr-only">{t('slicer.plate.printer')}</span>
        {#if current}
          <span class="truncate font-semibold text-foreground"
            >{current.name}</span
          >
          {@render tag(current)}
          <span
            class="hidden truncate font-mono text-xs text-muted-foreground @7xl/plate:inline"
            >{lineOf(current)}</span
          >
        {:else}
          <span>{t('slicer.plate.blocked.noPrinter')}</span>
        {/if}
        <ChevronDown aria-hidden="true" />
      </Button>
    {/snippet}
  </DropdownMenuTrigger>
  <DropdownMenuContent align="end" class="w-96">
    {#if printers.length === 0}
      <DropdownMenuLabel class="font-normal text-muted-foreground">
        {t('slicer.plate.noPrinters')}
      </DropdownMenuLabel>
    {:else}
      <DropdownMenuRadioGroup
        value={chosen ?? ''}
        onValueChange={(value: string) => {
          onChoose(value);
        }}
      >
        {#each printers as printer (printer.id)}
          <DropdownMenuRadioItem value={printer.id}>
            <span class="grid min-w-0 gap-0.5">
              <span class="flex min-w-0 items-center gap-2">
                <span class="truncate font-medium">{printer.name}</span>
                {@render tag(printer)}
              </span>
              <span class="truncate font-mono text-xs text-muted-foreground">
                {[lineOf(printer), bedOf(printer)].filter(Boolean).join(' · ')}
              </span>
            </span>
          </DropdownMenuRadioItem>
        {/each}
      </DropdownMenuRadioGroup>
    {/if}
  </DropdownMenuContent>
</DropdownMenu>
