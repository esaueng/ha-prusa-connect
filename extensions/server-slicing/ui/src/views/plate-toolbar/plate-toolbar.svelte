<!--
  The one row of tools over the 3D view: add a model; move, lay flat and
  arrange; undo and the view; then whatever the page puts at the end (the
  printer, the settings and the Print button), and the menu that leads to the
  versions, the jobs and the API.
-->
<script lang="ts">
  import {
    Box,
    ChevronDown,
    Copy,
    Ellipsis,
    LayoutGrid,
    Move,
    PanelBottom,
    Plus,
    Square,
    Undo2,
  } from '@lucide/svelte';
  import {
    Button,
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
    Input,
    Label,
    Separator,
    ToggleGroup,
    ToggleGroupItem,
  } from '@prusa/connect-ui/primitives';
  import type { Snippet } from 'svelte';

  import type { Translate } from '../../api';
  import {
    PLATE_MODES,
    type Plate,
    SCALE_PERCENT,
  } from '../../plate/plate.svelte';
  import type { ToolbarLink } from './types';

  let {
    plate,
    top,
    adding = false,
    links,
    t,
    onAdd,
    onView,
    onArrange,
    onCopy,
    trailing,
  }: {
    plate: Plate;
    /** The view looks straight down at the bed. */
    top: boolean;
    /** A model is being read, so Add waits. */
    adding?: boolean;
    /** The pages of this module the ⋯ menu leads to. */
    links: readonly ToolbarLink[];
    t: Translate;
    onAdd: () => void;
    onView: (top: boolean) => void;
    onArrange: (scope: 'all' | 'selection') => void;
    onCopy: () => void;
    trailing?: Snippet;
  } = $props();

  const id = $props.id();
  const none = $derived(plate.parts.length === 0);

  /** Applies a typed scale; a value the plate refuses puts the shown one back. */
  const scaled = (event: Event): void => {
    if (!(event.currentTarget instanceof HTMLInputElement)) return;
    const input = event.currentTarget;
    if (Number.isFinite(input.valueAsNumber) && input.validity.valid)
      plate.scale(input.valueAsNumber);
    input.value = String(plate.selectionScale ?? '');
  };
  const unselected = $derived(plate.selected.length === 0);
</script>

<div
  class="flex min-w-0 flex-wrap items-center gap-x-1 gap-y-1 border-b border-border bg-card px-2 py-1"
  role="toolbar"
  aria-label={t('slicer.plate.tools')}
  data-part="plate-toolbar"
>
  <Button variant="ghost" size="md" disabled={adding} onclick={onAdd}>
    <Plus aria-hidden="true" /><span class="@max-4xl/plate:sr-only"
      >{t('slicer.plate.add')}</span
    >
  </Button>

  <!-- A vertical separator fills its parent, so the parent sets its height. -->
  <div class="mx-1 flex h-6"><Separator orientation="vertical" /></div>

  <ToggleGroup
    type="single"
    value={plate.mode}
    onValueChange={(value: string) => {
      const mode = PLATE_MODES.find((one) => one === value);
      if (mode) plate.mode = mode;
    }}
  >
    <ToggleGroupItem value="move" disabled={none}>
      <Move aria-hidden="true" /><span class="@max-4xl/plate:sr-only"
        >{t('slicer.plate.move')}</span
      >
    </ToggleGroupItem>
    <ToggleGroupItem value="layflat" disabled={none}>
      <PanelBottom aria-hidden="true" /><span class="@max-4xl/plate:sr-only"
        >{t('slicer.plate.layFlat')}</span
      >
    </ToggleGroupItem>
  </ToggleGroup>

  <DropdownMenu>
    <DropdownMenuTrigger>
      {#snippet child({ props })}
        <Button {...props} variant="ghost" size="md" disabled={none}>
          <LayoutGrid aria-hidden="true" /><span class="@max-4xl/plate:sr-only"
            >{t('slicer.plate.arrange')}</span
          ><ChevronDown aria-hidden="true" />
        </Button>
      {/snippet}
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start">
      <DropdownMenuItem
        onSelect={() => {
          onArrange('all');
        }}><LayoutGrid />{t('slicer.plate.arrangeAll')}</DropdownMenuItem
      >
      <DropdownMenuItem
        disabled={unselected}
        onSelect={() => {
          onArrange('selection');
        }}><LayoutGrid />{t('slicer.plate.arrangeSelection')}</DropdownMenuItem
      >
      <DropdownMenuSeparator />
      <DropdownMenuItem disabled={unselected} onSelect={onCopy}
        ><Copy />{t('slicer.plate.copy')}</DropdownMenuItem
      >
    </DropdownMenuContent>
  </DropdownMenu>

  {#if !unselected}
    <div class="flex items-center gap-2 ps-1" data-part="plate-scale">
      <Label for="{id}-scale"
        ><span class="whitespace-nowrap">{t('slicer.plate.scale')}</span></Label
      >
      <!-- The input fills its box, so the box sets its width. -->
      <div class="w-20">
        <Input
          id="{id}-scale"
          type="number"
          min={SCALE_PERCENT.min}
          max={SCALE_PERCENT.max}
          step="any"
          value={plate.selectionScale ?? ''}
          onchange={scaled}
        />
      </div>
    </div>
  {/if}

  <!-- A vertical separator fills its parent, so the parent sets its height. -->
  <div class="mx-1 flex h-6"><Separator orientation="vertical" /></div>

  <Button
    variant="ghost"
    size="icon-md"
    label={t('slicer.plate.undo')}
    disabled={!plate.canUndo}
    onclick={() => {
      plate.undo();
    }}><Undo2 /></Button
  >
  <ToggleGroup
    type="single"
    value={top ? 'top' : 'angled'}
    onValueChange={(value: string) => {
      if (value) onView(value === 'top');
    }}
  >
    <ToggleGroupItem value="angled" aria-label={t('slicer.plate.angled')}
      ><Box aria-hidden="true" /></ToggleGroupItem
    >
    <ToggleGroupItem value="top" aria-label={t('slicer.plate.top')}
      ><Square aria-hidden="true" /></ToggleGroupItem
    >
  </ToggleGroup>

  <div class="ms-auto flex min-w-0 flex-wrap items-center justify-end gap-1">
    {@render trailing?.()}
    <DropdownMenu>
      <DropdownMenuTrigger>
        {#snippet child({ props })}
          <Button
            {...props}
            variant="ghost"
            size="icon-md"
            label={t('slicer.plate.more')}><Ellipsis /></Button
          >
        {/snippet}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {#each links as link (link.href)}
          <DropdownMenuItem>
            {#snippet child({ props })}
              <a {...props} href={link.href}>{link.label}</a>
            {/snippet}
          </DropdownMenuItem>
        {/each}
      </DropdownMenuContent>
    </DropdownMenu>
  </div>
</div>
