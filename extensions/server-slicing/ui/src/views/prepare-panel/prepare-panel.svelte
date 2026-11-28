<script lang="ts">
  import { Box, Crosshair, FileUp, RotateCcw } from '@lucide/svelte';
  import {
    Button,
    FaultAlert,
    Input,
    Label,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
  } from '@prusa/connect-ui/primitives';
  import { describeFault, type FaultText } from '@prusa/connect-ui/i18n';
  import { parseFileRef } from '@prusa/connect-ui/slots';
  import { onMount } from 'svelte';
  import type { BufferGeometry, Matrix4 } from 'three';

  import type { Job, STLBed, Translate } from '../../api';
  import { ModelLimitError } from './import-limits';
  import {
    bedCenter,
    fits,
    makeSTL,
    orientedModel,
    placedMatrix,
    readModel,
    upright,
    type Orientation,
  } from './model';
  import { placedName, type Destination, type PrepareApi } from './prepare-api';
  import { plateScene, type PlateScene } from './scene';

  let {
    beds = {},
    hasSlicer,
    source,
    api,
    t,
    onSliced,
  }: {
    beds?: Record<string, STLBed> | undefined;
    hasSlicer: boolean;
    source?: string | undefined;
    api: PrepareApi;
    t: Translate;
    onSliced: (job: Job) => void;
  } = $props();

  const id = $props.id();
  const controller = new AbortController();
  let preview = $state.raw<PlateScene>();
  let webglFailed = $state(false);
  let destinations = $state.raw<Destination[]>([]);
  let destinationsLoading = $state(true);
  let destinationFault = $state<FaultText>();
  let storageChoice = $state('');
  let profileChoice = $state('');
  let mesh = $state.raw<BufferGeometry>();
  let fileName = $state('');
  let orientation = $state<Orientation>({ ...upright });
  let position = $state<[number, number]>();
  let floor = $state(0);
  let modelFault = $state('');
  let actionFault = $state<FaultText>();
  let loading = $state(false);
  let busy = $state(false);
  let savedPath = $state('');
  let loadingVersion = 0;
  let draft:
    | {
        mesh: BufferGeometry;
        matrix: Matrix4;
        storage: string;
        name: string;
        saved: string;
      }
    | undefined;

  const profileNames = $derived(Object.keys(beds).sort());
  const profile = $derived(
    profileNames.includes(profileChoice)
      ? profileChoice
      : (profileNames[0] ?? ''),
  );
  const bed = $derived(beds[profile]);
  const storage = $derived(storageChoice || (destinations[0]?.name ?? ''));
  const storageLabel = $derived(
    destinations.find((one) => one.name === storage)?.label,
  );
  const xy: [number, number] = $derived(
    position ?? (bed ? bedCenter(bed) : [0, 0]),
  );
  const oriented = $derived(mesh && orientedModel(mesh, orientation, floor));
  const placement = $derived(oriented && placedMatrix(oriented.matrix, xy));
  const onBed = $derived(
    Boolean(bed && oriented && fits(oriented.bounds, xy, bed)),
  );
  const canSlice = $derived(
    hasSlicer &&
      onBed &&
      storage !== '' &&
      !busy &&
      !loading &&
      modelFault === '',
  );

  const angles = [
    { key: 'rx', label: 'slicer.prepare.rotateX' },
    { key: 'ry', label: 'slicer.prepare.rotateY' },
    { key: 'rz', label: 'slicer.prepare.rotateZ' },
  ] as const;

  const load = async (file: Promise<File> | File): Promise<void> => {
    const version = ++loadingVersion;
    loading = true;
    modelFault = '';
    actionFault = undefined;
    let reading = true;
    try {
      const loaded = await file;
      reading = false;
      const model = await readModel(loaded);
      const { geometry, placedAt } = model;
      if (controller.signal.aborted || loadingVersion !== version) {
        geometry.dispose();
        return;
      }
      mesh?.dispose();
      mesh = geometry;
      fileName = loaded.name;
      draft = undefined;
      savedPath = '';
      reset();
      position = placedAt;
      floor = model.floor;
    } catch (error) {
      if (!controller.signal.aborted && loadingVersion === version) {
        let key = reading
          ? 'slicer.prepare.storageReadFailed'
          : 'slicer.prepare.badModel';
        if (error instanceof ModelLimitError) key = 'slicer.prepare.modelLimit';
        modelFault = t(key);
      }
    } finally {
      if (loadingVersion === version) loading = false;
    }
  };

  const reset = (): void => {
    position = undefined;
    floor = 0;
    orientation = { ...upright };
  };

  const dropped = (event: DragEvent): void => {
    event.preventDefault();
    const file = event.dataTransfer?.files[0];
    if (file && !busy) void load(file);
  };

  const pick = (event: Event): void => {
    if (!(event.currentTarget instanceof HTMLInputElement)) return;
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = '';
    if (file && !busy) void load(file);
  };

  const changeNumber =
    (current: number, changed: (value: number) => void) =>
    (event: Event): void => {
      if (!(event.currentTarget instanceof HTMLInputElement)) return;
      const input = event.currentTarget;
      if (Number.isFinite(input.valueAsNumber) && input.validity.valid)
        changed(input.valueAsNumber);
      else input.value = String(current);
    };

  const browse = (): void => {
    document.getElementById(`${id}-model`)?.click();
  };

  const chooseProfile = (value: string): void => {
    profileChoice = value;
    position = undefined;
  };

  const chooseStorage = (value: string): void => {
    storageChoice = value;
  };

  const attachPreview = (
    canvas: HTMLCanvasElement,
  ): (() => void) | undefined => {
    try {
      const scene = plateScene(canvas, (xy) => {
        position = xy;
      });
      preview = scene;
      return () => scene.dispose();
    } catch {
      webglFailed = true;
      return undefined;
    }
  };

  const submit = async (): Promise<void> => {
    if (!canSlice || !mesh || !placement) return;
    busy = true;
    actionFault = undefined;
    savedPath = '';
    const selectedProfile = profile;
    try {
      if (
        draft?.mesh !== mesh ||
        !draft.matrix.equals(placement) ||
        draft.storage !== storage
      ) {
        draft = {
          mesh,
          matrix: placement,
          storage,
          name: placedName(fileName),
          saved: '',
        };
      }
      const { signal } = controller;
      if (!draft.saved)
        draft.saved = await api.save(
          draft.storage,
          draft.name,
          makeSTL(draft.mesh, draft.matrix),
          signal,
        );
      savedPath = draft.saved;
      const job = await api.submit(
        draft.storage,
        draft.saved,
        selectedProfile,
        signal,
      );
      if (!signal.aborted) onSliced(job);
    } catch (error) {
      if (!controller.signal.aborted) actionFault = describeFault(error);
    } finally {
      busy = false;
    }
  };

  $effect(() => {
    if (bed) preview?.bed(bed);
  });

  $effect(() => {
    preview?.model(mesh, placement, onBed, Boolean(bed) && !busy, xy);
  });

  onMount(() => {
    const { signal } = controller;
    const from = source;
    void api
      .destinations(signal)
      .then((values) => {
        if (signal.aborted) return;
        destinations = values;
        const candidate = from && parseFileRef(from)?.storage;
        if (candidate && values.some((one) => one.name === candidate))
          storageChoice = candidate;
      })
      .catch((error: unknown) => {
        if (!signal.aborted) destinationFault = describeFault(error);
      })
      .finally(() => {
        if (!signal.aborted) destinationsLoading = false;
      });

    if (from) void load(api.read(from, signal));

    return () => {
      controller.abort();
      mesh?.dispose();
    };
  });
</script>

<div class="@container grid gap-4" data-part="prepare-panel">
  <FaultAlert fault={destinationFault} />
  <FaultAlert fault={actionFault} />
  {#if actionFault && savedPath}
    <p class="text-sm break-all text-muted-foreground">
      {t('slicer.prepare.saved', { path: savedPath })}
    </p>
  {/if}

  <div class="workspace-grid grid min-w-0 gap-4">
    <section
      class="plate-shell grid min-w-0 overflow-hidden rounded-md border border-border bg-card"
      aria-label={t('slicer.prepare.plate')}
    >
      <header
        class="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2"
      >
        <h2 class="font-medium">{t('slicer.prepare.plate')}</h2>
        <div class="flex flex-wrap items-center gap-1">
          <Button
            variant="ghost"
            size="icon-sm"
            label={t('slicer.prepare.perspective')}
            disabled={!bed || webglFailed}
            onclick={() => preview?.view()}><Box /></Button
          >
          <Button
            variant="ghost"
            size="icon-sm"
            label={t('slicer.prepare.top')}
            disabled={!bed || webglFailed}
            onclick={() => preview?.view(true)}><Crosshair /></Button
          >
          <Button variant="secondary" disabled={busy} onclick={browse}
            ><FileUp />{t('slicer.prepare.open')}</Button
          >
        </div>
      </header>

      <Input
        id="{id}-model"
        type="file"
        accept=".stl,.3mf"
        class="hidden"
        onchange={pick}
      />

      <div
        class="plate-stage relative grid min-h-96 min-w-0 place-items-center overflow-hidden bg-muted/50"
        role="region"
        aria-label={t('slicer.prepare.plate')}
        ondragover={(event) => event.preventDefault()}
        ondrop={dropped}
      >
        <canvas
          class="absolute inset-0 size-full touch-none"
          aria-label={t('slicer.prepare.view')}
          {@attach attachPreview}
        ></canvas>
        {#if loading || !mesh || webglFailed}
          <div
            class="pointer-events-none z-10 m-4 grid max-w-68 place-items-center gap-2 rounded-md border border-border bg-card p-4 text-center text-sm"
            aria-live="polite"
          >
            <FileUp class="size-5 text-muted-foreground" />
            {#if loading}
              {t('slicer.prepare.reading')}
            {:else if webglFailed}
              {t('slicer.prepare.noWebGL')}
            {:else}
              {t('slicer.prepare.drop')}
            {/if}
          </div>
        {/if}
        {#if bed}
          <div
            class="pointer-events-none absolute end-3 bottom-3 rounded-sm bg-card px-2 py-1 text-xs text-muted-foreground"
          >
            {t('slicer.prepare.bedSize', {
              width: String(bed.max_x - bed.min_x),
              depth: String(bed.max_y - bed.min_y),
              height: String(bed.height),
            })}
          </div>
        {/if}
      </div>
      {#if modelFault}
        <FaultAlert
          fault={{ code: undefined, title: modelFault, detail: undefined }}
        />
      {/if}
    </section>

    <aside
      class="grid min-w-0 content-start gap-4 rounded-md border border-border bg-card p-4"
    >
      <div class="grid gap-2">
        <Label for="{id}-profile">{t('slicer.prepare.profile')}</Label>
        {#if profileNames.length > 0}
          <Select value={profile} onValueChange={chooseProfile}>
            <SelectTrigger id="{id}-profile" disabled={busy}>
              <SelectValue>{profile}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {#each profileNames as name (name)}
                <SelectItem value={name} label={name}>{name}</SelectItem>
              {/each}
            </SelectContent>
          </Select>
        {:else}
          <p class="text-sm text-muted-foreground">
            {t('slicer.prepare.noProfiles')}
          </p>
        {/if}
        {#if !hasSlicer}
          <p class="text-sm text-muted-foreground">
            {t('slicer.prepare.noSlicer')}
          </p>
        {/if}
      </div>

      <fieldset
        class="grid min-w-0 gap-3 border-t border-border pt-3"
        disabled={!mesh || !bed || busy}
      >
        <legend class="sr-only">{t('slicer.prepare.placement')}</legend>
        <div class="flex items-center justify-between gap-2">
          <h2 class="font-medium">{t('slicer.prepare.placement')}</h2>
          <Button
            variant="ghost"
            size="icon-sm"
            label={t('slicer.prepare.reset')}
            onclick={reset}><RotateCcw /></Button
          >
        </div>
        {#if fileName}
          <p class="truncate text-sm text-muted-foreground" title={fileName}>
            {fileName}
          </p>
        {/if}
        <div class="grid grid-cols-2 gap-2">
          <div class="grid gap-1">
            <Label for="{id}-x">{t('slicer.prepare.x')}</Label>
            <Input
              id="{id}-x"
              type="number"
              step="any"
              value={xy[0]}
              onchange={changeNumber(xy[0], (value) => {
                position = [value, xy[1]];
              })}
            />
          </div>
          <div class="grid gap-1">
            <Label for="{id}-y">{t('slicer.prepare.y')}</Label>
            <Input
              id="{id}-y"
              type="number"
              step="any"
              value={xy[1]}
              onchange={changeNumber(xy[1], (value) => {
                position = [xy[0], value];
              })}
            />
          </div>
          {#each angles as angle (angle.key)}
            <div class="grid gap-1">
              <Label for="{id}-{angle.key}">{t(angle.label)}</Label>
              <Input
                id="{id}-{angle.key}"
                type="number"
                min="-360"
                max="360"
                step="any"
                value={orientation[angle.key]}
                onchange={changeNumber(orientation[angle.key], (value) => {
                  orientation[angle.key] = value;
                })}
              />
            </div>
          {/each}
          <div class="grid gap-1">
            <Label for="{id}-scale">{t('slicer.prepare.scale')}</Label>
            <Input
              id="{id}-scale"
              type="number"
              min="1"
              max="1000"
              step="any"
              value={orientation.scale}
              onchange={changeNumber(orientation.scale, (value) => {
                orientation.scale = value;
              })}
            />
          </div>
        </div>
        {#if mesh && bed && !onBed}
          <p class="text-sm text-destructive" role="status">
            {t('slicer.prepare.outside')}
          </p>
        {/if}
      </fieldset>

      <div class="grid gap-2 border-t border-border pt-3">
        <Label for="{id}-storage">{t('slicer.prepare.destination')}</Label>
        {#if destinations.length > 0}
          <Select value={storage} onValueChange={chooseStorage}>
            <SelectTrigger id="{id}-storage" disabled={busy}>
              <SelectValue>{storageLabel}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {#each destinations as one (one.name)}
                <SelectItem value={one.name} label={one.label}
                  >{one.label}</SelectItem
                >
              {/each}
            </SelectContent>
          </Select>
        {:else if !destinationsLoading && !destinationFault}
          <p class="text-sm text-muted-foreground">
            {t('slicer.prepare.noStorage')}
          </p>
        {/if}
        <p class="text-xs text-muted-foreground">
          {t('slicer.prepare.saveHint')}
        </p>
        <Button disabled={!canSlice} onclick={() => void submit()}
          >{t(busy ? 'slicer.prepare.saving' : 'slicer.prepare.slice')}</Button
        >
      </div>
    </aside>
  </div>
</div>

<style>
  .plate-shell {
    grid-template-rows: auto 1fr auto;
  }

  @container (width >= 54rem) {
    .workspace-grid {
      grid-template-columns: minmax(0, 1fr) minmax(17rem, 21rem);
    }

    .plate-stage {
      min-block-size: 34rem;
    }
  }
</style>
