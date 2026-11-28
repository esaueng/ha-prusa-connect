<!--
  The print settings beside the printer: layer height, material, supports,
  infill, walls and brim, plus where the files are saved. The material is a
  saved profile, which carries the printer's and the filament's settings. The
  rest change that profile for this plate, and the page keeps them for the
  printer they were chosen for.
-->
<script lang="ts">
  import { ChevronDown, Minus, Plus, SlidersHorizontal } from '@lucide/svelte';
  import { formatNumber } from '@prusa/connect-ui/i18n';
  import {
    Button,
    Label,
    Popover,
    PopoverContent,
    PopoverTrigger,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Slider,
    Switch,
    ToggleGroup,
    ToggleGroupItem,
  } from '@prusa/connect-ui/primitives';

  import type { Translate } from '../../api';
  import {
    BRIM_MM,
    LAYER_HEIGHTS_MM,
    type PlateChoices,
    type PrintSettings,
    type ProfileSettings,
    settingsShown,
    SUPPORTS,
  } from '../../plate/print-settings';
  import type { Destination } from '../prepare-panel/prepare-api';

  let {
    choices,
    profile,
    profiles,
    destinations,
    printerName,
    bedNote,
    t,
    onChange,
  }: {
    choices: PlateChoices;
    /**
     * The profile the plate slices with, which the workspace chooses: the one
     * kept for the printer, or one whose bed is the printer's size.
     */
    profile: string | undefined;
    /** Each saved profile and what it prints with, by name. */
    profiles: Readonly<Record<string, ProfileSettings>>;
    destinations: readonly Destination[];
    printerName: string | undefined;
    /** Why the profile's bed does not fit the printer, when it does not. */
    bedNote?: string | undefined;
    t: Translate;
    onChange: (choices: PlateChoices) => void;
  } = $props();

  const id = $props.id();
  const names = $derived(Object.keys(profiles).sort());
  const shown = $derived(
    settingsShown(profile ? profiles[profile] : undefined, choices.settings),
  );
  const storage = $derived(
    destinations.find((one) => one.name === choices.storage) ?? destinations[0],
  );
  const layers = $derived(
    [
      ...new Set([
        ...LAYER_HEIGHTS_MM,
        ...(shown.layer_height_mm === undefined ? [] : [shown.layer_height_mm]),
      ]),
    ].sort((a, b) => a - b),
  );

  const millimetres = (value: number): string =>
    formatNumber(value, { minimumFractionDigits: 2, maximumFractionDigits: 2 });

  const materialOf = (name: string): string =>
    [profiles[name]?.filament_type, name].filter(Boolean).join(' · ');

  const summary = $derived(
    [
      shown.layer_height_mm === undefined
        ? undefined
        : `${millimetres(shown.layer_height_mm)} mm`,
      profile ? profiles[profile]?.filament_type : undefined,
      shown.infill_percent === undefined
        ? undefined
        : formatNumber(shown.infill_percent / 100, { style: 'percent' }),
    ]
      .filter(Boolean)
      .join(' · '),
  );

  const change = (settings: PrintSettings): void => {
    onChange({ ...choices, settings: { ...choices.settings, ...settings } });
  };

  const walls = $derived(shown.walls ?? 2);

  /** The brim the switch turns on: the profile's own, or BRIM_MM. */
  const brimOn = (): number => {
    const own = profile ? profiles[profile]?.brim_width_mm : undefined;
    return own !== undefined && own > 0 ? own : BRIM_MM;
  };
</script>

<Popover>
  <PopoverTrigger>
    {#snippet child({ props })}
      <Button
        {...props}
        variant="ghost"
        size="md"
        class="min-w-0"
        data-part="print-settings"
      >
        <SlidersHorizontal aria-hidden="true" />
        <span class="sr-only">{t('slicer.plate.settings')}</span>
        <span
          class="truncate font-mono text-xs text-foreground @max-2xl/plate:hidden"
          >{summary}</span
        >
        <ChevronDown aria-hidden="true" />
      </Button>
    {/snippet}
  </PopoverTrigger>
  <PopoverContent
    align="end"
    class="grid w-md max-w-(--radix-popper-available-width) gap-0 p-0"
  >
    <header class="grid gap-0.5 border-b border-border px-4 py-3">
      <h2 class="text-sm font-semibold">{t('slicer.plate.settings')}</h2>
      {#if printerName}
        <p class="text-xs text-muted-foreground">
          {t('slicer.plate.settingsFor', { printer: printerName })}
        </p>
      {/if}
    </header>

    <div class="settings-grid grid items-center gap-x-4 gap-y-3 px-4 py-3">
      <span id="{id}-layer" class="text-sm">{t('slicer.plate.layer')}</span>
      <ToggleGroup
        type="single"
        variant="outline"
        aria-labelledby="{id}-layer"
        value={shown.layer_height_mm === undefined
          ? ''
          : String(shown.layer_height_mm)}
        onValueChange={(value: string) => {
          if (value) change({ layer_height_mm: Number(value) });
        }}
      >
        {#each layers as layer (layer)}
          <ToggleGroupItem value={String(layer)} class="font-mono text-xs"
            >{millimetres(layer)}</ToggleGroupItem
          >
        {/each}
      </ToggleGroup>

      <Label for="{id}-material">{t('slicer.plate.material')}</Label>
      <Select
        value={profile ?? ''}
        onValueChange={(value: string) => {
          onChange({ ...choices, profile: value });
        }}
      >
        <SelectTrigger
          id="{id}-material"
          class="w-full"
          disabled={names.length === 0}
        >
          <SelectValue>{profile ? materialOf(profile) : ''}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {#each names as name (name)}
            <SelectItem value={name} label={materialOf(name)}
              >{materialOf(name)}</SelectItem
            >
          {/each}
        </SelectContent>
      </Select>

      <span id="{id}-supports" class="text-sm"
        >{t('slicer.plate.supports')}</span
      >
      <ToggleGroup
        type="single"
        variant="outline"
        aria-labelledby="{id}-supports"
        value={shown.supports ?? ''}
        onValueChange={(value: string) => {
          const supports = SUPPORTS.find((one) => one === value);
          if (supports) change({ supports });
        }}
      >
        {#each SUPPORTS as supports (supports)}
          <ToggleGroupItem value={supports} class="text-xs"
            >{t(`slicer.plate.supports.${supports}`)}</ToggleGroupItem
          >
        {/each}
      </ToggleGroup>

      <span id="{id}-infill" class="text-sm">{t('slicer.plate.infill')}</span>
      <div class="flex items-center gap-3">
        <Slider
          type="single"
          min={0}
          max={100}
          step={5}
          aria-labelledby="{id}-infill"
          value={shown.infill_percent ?? 0}
          onValueChange={(value: number) => {
            change({ infill_percent: value });
          }}
          class="flex-1"
        />
        <span class="w-10 text-end font-mono text-xs tabular-nums">
          {formatNumber((shown.infill_percent ?? 0) / 100, {
            style: 'percent',
          })}
        </span>
      </div>

      <span id="{id}-walls" class="text-sm">{t('slicer.plate.walls')}</span>
      <div
        class="flex items-center gap-1"
        role="group"
        aria-labelledby="{id}-walls"
      >
        <Button
          variant="secondary"
          size="icon-sm"
          label={t('slicer.plate.fewerWalls')}
          disabled={walls <= 1}
          onclick={() => {
            change({ walls: walls - 1 });
          }}><Minus /></Button
        >
        <output class="w-8 text-center font-mono text-sm tabular-nums"
          >{walls}</output
        >
        <Button
          variant="secondary"
          size="icon-sm"
          label={t('slicer.plate.moreWalls')}
          disabled={walls >= 20}
          onclick={() => {
            change({ walls: walls + 1 });
          }}><Plus /></Button
        >
      </div>

      <Label for="{id}-brim">{t('slicer.plate.brim')}</Label>
      <Switch
        id="{id}-brim"
        checked={(shown.brim_width_mm ?? 0) > 0}
        onCheckedChange={(on: boolean) => {
          change({ brim_width_mm: on ? brimOn() : 0 });
        }}
      />

      <Label for="{id}-storage">{t('slicer.plate.storage')}</Label>
      <Select
        value={storage?.name ?? ''}
        onValueChange={(value: string) => {
          onChange({ ...choices, storage: value });
        }}
      >
        <SelectTrigger
          id="{id}-storage"
          class="w-full"
          disabled={destinations.length === 0}
        >
          <SelectValue>{storage?.label ?? ''}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {#each destinations as one (one.name)}
            <SelectItem value={one.name} label={one.label}
              >{one.label}</SelectItem
            >
          {/each}
        </SelectContent>
      </Select>
    </div>

    <footer
      class="flex items-center justify-between gap-3 border-t border-border px-4 py-2"
    >
      <p class="min-w-0 truncate font-mono text-xs text-muted-foreground">
        {#if bedNote}{bedNote}{:else}{profile ?? ''}{/if}
      </p>
      <Button
        variant="link"
        onclick={() => {
          onChange({ ...choices, settings: {} });
        }}>{t('slicer.plate.reset')}</Button
      >
    </footer>
  </PopoverContent>
</Popover>

<style>
  .settings-grid {
    grid-template-columns: auto minmax(0, 1fr);
  }
</style>
