<!--
  The build-plate workspace the Slicing page opens on: one row of tools over a
  3D view of the chosen printer's bed, and a status line under it. Parts are
  added from files or from Storage, placed, laid flat and arranged, then
  sliced on this server and sent to the printer.
-->
<script lang="ts">
  import { FileUp } from '@lucide/svelte';
  import {
    describeFault,
    type FaultText,
    type MessageParams,
  } from '@prusa/connect-ui/i18n';
  import { FaultAlert, Input, Label } from '@prusa/connect-ui/primitives';
  import type { Device } from '@prusa/connect-ui/domain';
  import { parseFileRef } from '@prusa/connect-ui/slots';
  import { onMount, untrack } from 'svelte';

  import type { STLBed, Translate } from '../../api';
  import { footprintOf, worldMatrix } from '../../plate/part';
  import { PlateFlow, signatureOf } from '../../plate/plate-flow.svelte';
  import { plateScene, type PlateScene } from '../../plate/plate-scene';
  import { Plate } from '../../plate/plate.svelte';
  import type { SendSnapshot } from '../../plate/sendable';
  import {
    checkedSettings,
    choicesKey,
    type PlateChoices,
    PRINTER_KEY,
    type ProfileSettings,
    readChoices,
    unchanged,
    writeChoices,
  } from '../../plate/print-settings';
  import {
    decidePrint,
    plateBlockOf,
    printerBlockOf,
    printerChoicesOf,
  } from '../../plate/printers';
  import { PlateStatus } from '../plate-status';
  import { PlateToolbar, type ToolbarLink } from '../plate-toolbar';
  import { PrintButton } from '../print-button';
  import { PrintSettingsMenu } from '../print-settings';
  import { PrinterPicker } from '../printer-picker';
  import { ModelLimitError } from './import-limits';
  import {
    STEP_TOLERANCE_MM,
    StepError,
    StepMeshLimitError,
  } from './import-step';
  import { readMeshFile } from './mesh-import';
  import type { Destination, PrepareApi } from './prepare-api';
  import { ThreeMFRoleError } from './three-mf-roles';

  /** What a page remembers for this person between visits. */
  interface Remember {
    read(key: string): string | null;
    write(key: string, value: string): void;
  }

  let {
    beds = {},
    profileSettings = {},
    hasSlicer,
    source,
    api,
    devices,
    remember,
    links = [],
    t,
  }: {
    /** The rectangular print volume of each saved profile, by name. */
    beds?: Readonly<Record<string, STLBed>> | undefined;
    /** What each saved profile prints with, by name. */
    profileSettings?: Readonly<Record<string, ProfileSettings>> | undefined;
    hasSlicer: boolean;
    /** A model in Storage to open, as `<storage>:<path>`. */
    source?: string | undefined;
    api: PrepareApi;
    /** The printers the person may see, as the host's fleet lists them. */
    devices: readonly Device[];
    remember: Remember;
    links?: readonly ToolbarLink[];
    t: Translate;
  } = $props();

  // The file types the readers in mesh-import.ts take.
  const ACCEPT = '.stl,.3mf,.step,.stp';

  const id = $props.id();
  const controller = new AbortController();
  // The API and the store a workspace is mounted with stay its own.
  const flow = new PlateFlow(
    untrack(() => api),
    controller.signal,
  );
  let root = $state<HTMLElement>();
  let scene = $state.raw<PlateScene>();
  let webglFailed = $state(false);
  let top = $state(false);
  /** How many batches of files are waiting or being read. */
  let batches = $state(0);
  let reads: Promise<void> = Promise.resolve();
  const loading = $derived(batches > 0);
  let modelFaults = $state.raw<readonly string[]>([]);
  /** The STEP mesh tolerance for files added from now on, in mm. */
  let tolerance = $state(STEP_TOLERANCE_MM);
  /** A STEP file was added or refused, so its tolerance is shown. */
  let stepUsed = $state(false);
  let notice = $state<{ key: string; params: MessageParams }>();
  let destinations = $state.raw<Destination[]>([]);
  let destinationFault = $state<FaultText>();
  let printerChoice = $state(
    untrack(() => remember.read(PRINTER_KEY)) ?? undefined,
  );
  let choicesFor = $state.raw<Readonly<Record<string, PlateChoices>>>({});

  const printers = $derived(printerChoicesOf(devices));
  const printer = $derived(
    printers.find((one) => one.id === printerChoice) ?? printers[0],
  );
  const choices = $derived(
    choicesFor[printer?.id ?? ''] ??
      readChoices(remember.read(choicesKey(printer?.id))),
  );
  const profileNames = $derived(Object.keys(beds).sort());
  const sameSize = (a: STLBed, b: STLBed): boolean =>
    Math.abs(a.max_x - a.min_x - (b.max_x - b.min_x)) < 1 &&
    Math.abs(a.max_y - a.min_y - (b.max_y - b.min_y)) < 1;
  // A profile kept for this printer, else one whose bed is the printer's size.
  const profile = $derived.by(() => {
    if (choices.profile && profileNames.includes(choices.profile))
      return choices.profile;
    const printerBed = printer?.bed;
    const fitting = printerBed
      ? profileNames.find((name) => {
          const bed = beds[name];
          return bed !== undefined && sameSize(bed, printerBed);
        })
      : undefined;
    return fitting ?? profileNames[0];
  });
  const profileBed = $derived(profile ? beds[profile] : undefined);
  // The volume the parts must stand in: the printer's, placed where the
  // profile's bed starts, and no larger than the profile's bed, which is what
  // the slicer checks against.
  const bed = $derived.by((): STLBed | undefined => {
    const own = printer?.bed;
    if (!own) return profileBed;
    const minX = profileBed?.min_x ?? 0;
    const minY = profileBed?.min_y ?? 0;
    return {
      min_x: minX,
      min_y: minY,
      max_x: Math.min(minX + own.max_x, profileBed?.max_x ?? Infinity),
      max_y: Math.min(minY + own.max_y, profileBed?.max_y ?? Infinity),
      height: Math.min(own.height, profileBed?.height ?? Infinity),
    };
  });
  const bedNote = $derived(
    printer?.bed && profileBed && !sameSize(profileBed, printer.bed)
      ? t('slicer.plate.bedDiffers', {
          width: String(profileBed.max_x - profileBed.min_x),
          depth: String(profileBed.max_y - profileBed.min_y),
        })
      : undefined,
  );
  const plate = new Plate(() => bed);
  const storage = $derived(
    destinations.find((one) => one.name === choices.storage)?.name ??
      destinations[0]?.name ??
      '',
  );
  const settings = $derived(
    unchanged(choices.settings) ? undefined : checkedSettings(choices.settings),
  );
  const meshes = $derived(
    plate.parts.map((part) => ({
      id: part.id,
      geometry: part.geometry,
      matrix: worldMatrix(part),
    })),
  );
  const key = $derived(String(plate.revision));
  const signature = $derived(
    signatureOf({ meshKey: key, profile: profile ?? '', settings, storage }),
  );
  const sliced = $derived(flow.slicedFor(signature));
  const readiness = $derived({
    parts: plate.parts.length,
    outside: plate.outside.length,
    profile: profile !== undefined,
    slicer: hasSlicer,
    storage: storage !== '',
    busy: flow.busy || loading,
  });
  const decision = $derived(
    decidePrint(printer, readiness, flow.queueBlock(printer?.id)),
  );
  const queueTarget = $derived(
    decision.action === 'queue' && printer?.mayPrint ? printer.id : undefined,
  );
  const waiting = $derived(printer ? flow.waitingFor(printer.id) : undefined);
  const flowFault = $derived.by(() => {
    const fault = flow.fault;
    if (!fault?.code?.startsWith('server-slicing/')) return fault;
    const key = `error.${fault.code}`;
    const title = t(key);
    if (title === key || title === fault.title) return fault;
    return { ...fault, title, detail: fault.detail ?? fault.title };
  });
  const canSlice = $derived(plateBlockOf(readiness) === undefined);
  const selectedName = $derived(
    plate.selection.length === 1 ? plate.selection[0]?.name : undefined,
  );

  const input = () => ({
    meshKey: key,
    name: plate.parts[0]?.name ?? 'plate.stl',
    meshes,
    storage,
    profile: profile ?? '',
    settings,
  });

  const choose = (next: string): void => {
    printerChoice = next;
    remember.write(PRINTER_KEY, next);
  };

  const checkQueue = (signal: AbortSignal): void => {
    if (queueTarget) void flow.checkQueue(queueTarget, signal);
  };

  const keep = (next: PlateChoices): void => {
    const owner = printer?.id ?? '';
    choicesFor = { ...choicesFor, [owner]: next };
    remember.write(choicesKey(printer?.id), writeChoices(next));
  };

  /**
   * Reads files onto the plate as one batch, in the order given. A batch that
   * arrives while another is read waits for it, so every file is added. A
   * file that cannot be read is named in the faults, and the others are still
   * added.
   */
  const load = (files: readonly (Promise<File> | File)[]): Promise<void> => {
    if (batches === 0) {
      modelFaults = [];
      notice = undefined;
    }
    batches += 1;
    const read = reads.then(() => readBatch(files));
    reads = read.finally(() => {
      batches -= 1;
    });
    return read;
  };

  /** True once the workspace closed. A function, so each wait reads it anew. */
  const closed = (): boolean => controller.signal.aborted;

  const readBatch = async (
    files: readonly (Promise<File> | File)[],
  ): Promise<void> => {
    const faults: string[] = [];
    let threeMF = false;
    for (const file of files) {
      // A batch that waited for another may start after the page closed.
      if (closed()) return;
      let reading = true;
      let name = '';
      try {
        const loaded = await file;
        if (closed()) return;
        reading = false;
        name = loaded.name;
        stepUsed ||= isStep(loaded.name);
        // The workspace's signal stops a STEP worker when the page closes.
        const imported = await readMeshFile(loaded, {
          tolerance,
          signal: controller.signal,
        });
        if (closed()) {
          for (const part of imported.parts) part.geometry.dispose();
          return;
        }
        if (plate.add(imported, loaded.name)) {
          faults.push(
            t('slicer.plate.fileFault', {
              file: loaded.name,
              reason: t('slicer.plate.notice.tooManyTriangles'),
            }),
          );
          continue;
        }
        threeMF ||= loaded.name.toLowerCase().endsWith('.3mf');
        if (imported.bodies !== undefined && imported.bodies > 1)
          notice = {
            key: 'slicer.prepare.bodies',
            params: { count: imported.bodies },
          };
      } catch (error) {
        if (closed()) return;
        const reason = reasonOf(error, reading);
        faults.push(
          name ? t('slicer.plate.fileFault', { file: name, reason }) : reason,
        );
      }
    }
    if (faults.length > 0) modelFaults = [...modelFaults, ...faults];
    if (threeMF) notice = { key: 'slicer.prepare.geometryOnly', params: {} };
  };

  const isStep = (name: string): boolean => /\.(?:step|stp)$/iu.test(name);

  const reasonOf = (error: unknown, reading: boolean): string => {
    if (error instanceof StepError)
      return error.reason === 'empty'
        ? t('slicer.prepare.noSolids')
        : t('slicer.prepare.badStep', { reason: error.message });
    if (error instanceof StepMeshLimitError)
      return t(
        error.reason === 'time'
          ? 'slicer.prepare.tooSlow'
          : 'slicer.prepare.tooDense',
      );
    return t(faultKey(error, reading));
  };

  const changeTolerance = (event: Event): void => {
    if (!(event.currentTarget instanceof HTMLInputElement)) return;
    const input = event.currentTarget;
    if (Number.isFinite(input.valueAsNumber) && input.validity.valid)
      tolerance = input.valueAsNumber;
    else input.value = String(tolerance);
  };

  const faultKey = (error: unknown, reading: boolean): string => {
    if (error instanceof ModelLimitError) return 'slicer.prepare.modelLimit';
    if (error instanceof ThreeMFRoleError) return 'slicer.prepare.partRoles';
    return reading
      ? 'slicer.prepare.storageReadFailed'
      : 'slicer.prepare.badModel';
  };

  const browse = (): void => {
    document.getElementById(`${id}-model`)?.click();
  };

  const pick = (event: Event): void => {
    if (!(event.currentTarget instanceof HTMLInputElement)) return;
    const files = [...(event.currentTarget.files ?? [])];
    event.currentTarget.value = '';
    if (files.length > 0) void load(files);
  };

  const dropped = (event: DragEvent): void => {
    event.preventDefault();
    const files = [...(event.dataTransfer?.files ?? [])];
    if (files.length > 0) void load(files);
  };

  const arrangeParts = (scope: 'all' | 'selection'): void => {
    const count = scope === 'all' ? plate.parts.length : plate.selected.length;
    const unplaced = plate.arrange(scope);
    notice = unplaced
      ? { key: 'slicer.plate.notice.unplaced', params: { count: unplaced } }
      : { key: 'slicer.plate.notice.arranged', params: { count } };
  };

  const copy = (): void => {
    const added = plate.copy();
    if (added === 'noRoom')
      notice = { key: 'slicer.plate.notice.noRoom', params: {} };
    else if (added === 'tooManyTriangles')
      notice = { key: 'slicer.plate.notice.tooManyTriangles', params: {} };
    else if (added > 0)
      notice = { key: 'slicer.plate.notice.copied', params: { count: added } };
  };

  /** The plate and the printer as a send depends on them, read when asked. */
  const now = (): SendSnapshot => ({
    plate: JSON.stringify([signature, bed, printer?.bed]),
    printer: printer?.id,
    state: printer?.state,
    action: decision.action,
    sendable:
      plate.printable &&
      printerBlockOf(printer, flow.queueBlock(printer?.id)) === undefined,
  });

  const print = async (): Promise<void> => {
    notice = undefined;
    if (decision.block !== undefined || !printer) return;
    // Keep the chosen printer when live updates reorder the list.
    if (printerChoice !== printer.id) choose(printer.id);
    if (decision.action === 'queue') await flow.enqueue(input(), printer, now);
    else await flow.print(input(), printer, now);
  };

  const sliceOnly = async (): Promise<void> => {
    notice = undefined;
    if (canSlice) await flow.slice(input());
  };

  const saveStl = async (): Promise<void> => {
    notice = undefined;
    if (plate.parts.length > 0 && storage) await flow.save(input());
  };

  const download = async (): Promise<void> => {
    notice = undefined;
    if (!canSlice) return;
    const file = await flow.download(input());
    if (!file) return;
    const href = URL.createObjectURL(file);
    const link = document.createElement('a');
    link.href = href;
    link.download = file.name;
    link.click();
    URL.revokeObjectURL(href);
  };

  const attach = (canvas: HTMLCanvasElement): (() => void) | undefined => {
    try {
      const made = plateScene(canvas, {
        press: (part, additive) => {
          plate.press(part, additive);
        },
        dragStart: () => {
          plate.beginMove();
        },
        drag: (dx, dy) => {
          plate.moveBy(dx, dy);
        },
        dragEnd: () => {
          plate.endMove();
        },
        layFlat: (part, face) => {
          plate.layFlat(part, face);
        },
      });
      scene = made;
      return () => {
        made.dispose();
      };
    } catch {
      webglFailed = true;
      return undefined;
    }
  };

  /** A key typed into a field, a menu or a dialog is not the plate's. */
  const typing = (target: EventTarget | null): boolean =>
    target instanceof Element &&
    target.closest(
      'input, textarea, select, [contenteditable], [role="menu"], [role="dialog"], [role="listbox"]',
    ) !== null;

  const keyed = (event: KeyboardEvent): void => {
    if (event.defaultPrevented || typing(event.target)) return;
    const onPage =
      event.target === document.body ||
      (event.target instanceof Node && root?.contains(event.target) === true);
    if (!onPage) return;
    const command = event.metaKey || event.ctrlKey;
    if (command && event.key.toLowerCase() === 'z' && !event.shiftKey) {
      event.preventDefault();
      plate.undo();
    } else if (command || event.altKey) {
      return;
    } else if (event.key === 'r' || event.key === 'R') {
      plate.rotate();
    } else if (event.key === 'Delete' || event.key === 'Backspace') {
      event.preventDefault();
      plate.remove();
    } else if (event.key === 'Escape') {
      if (plate.mode === 'layflat') plate.mode = 'move';
      else plate.clearSelection();
    }
  };

  $effect(() => {
    if (bed) scene?.bed(bed);
  });

  $effect(() => {
    const outside = plate.outside;
    const selected = plate.selected;
    scene?.parts(
      plate.parts.map((part) => ({
        id: part.id,
        geometry: part.geometry,
        matrix: worldMatrix(part),
        rect: footprintOf(part),
        selected: selected.includes(part.id),
        outside: outside.includes(part.id),
      })),
    );
  });

  $effect(() => {
    scene?.mode(plate.mode);
  });

  $effect(() => {
    // A listener on the window, because the keys reach the body when nothing
    // on the page has focus. It ends with the workspace.
    window.addEventListener('keydown', keyed);
    return () => {
      window.removeEventListener('keydown', keyed);
    };
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
        if (
          candidate &&
          !choices.storage &&
          values.some((one) => one.name === candidate)
        )
          keep({ ...choices, storage: candidate });
      })
      .catch((error: unknown) => {
        if (!signal.aborted) destinationFault = describeFault(error);
      });

    if (from) void load([api.read(from, signal)]);

    return () => {
      controller.abort();
      plate.dispose();
    };
  });

  const noticeText = $derived.by(() => {
    const shown = flow.notice ?? notice;
    return shown ? t(shown.key, shown.params) : undefined;
  });
  const blocked = $derived(
    decision.block && !(decision.block === 'busy' && flow.busy)
      ? t(`slicer.plate.blocked.${decision.block}`)
      : undefined,
  );
</script>

<div
  bind:this={root}
  class="workspace @container/plate grid min-w-0 bg-background"
  data-part="prepare-panel"
>
  <PlateToolbar
    {plate}
    {top}
    adding={loading}
    {links}
    {t}
    onAdd={browse}
    onView={(next: boolean) => {
      top = next;
      scene?.view(next);
    }}
    onArrange={arrangeParts}
    onCopy={copy}
  >
    {#snippet trailing()}
      {#if stepUsed}
        <div class="flex items-center gap-2" data-part="step-tolerance">
          <Label for="{id}-tolerance"
            ><span class="whitespace-nowrap"
              >{t('slicer.prepare.tolerance')}</span
            ></Label
          >
          <!-- The input fills its box, so the box sets its width. -->
          <div class="w-20">
            <Input
              id="{id}-tolerance"
              type="number"
              min="0.001"
              max="1"
              step="any"
              title={t('slicer.prepare.toleranceHint')}
              value={tolerance}
              onchange={changeTolerance}
            />
          </div>
        </div>
      {/if}
      <PrinterPicker {printers} chosen={printer?.id} {t} onChoose={choose} />
      <PrintSettingsMenu
        {choices}
        {profile}
        profiles={Object.fromEntries(
          profileNames.map((name) => [name, profileSettings[name] ?? {}]),
        )}
        {destinations}
        printerName={printer?.name}
        {bedNote}
        {t}
        onChange={keep}
      />
      {#key queueTarget}
        <PrintButton
          {decision}
          estimate={sliced?.result?.estimated_seconds}
          step={flow.step}
          {canSlice}
          canSave={plate.parts.length > 0 && storage !== '' && !flow.busy}
          describedBy="{id}-blocked"
          {t}
          onCheckQueue={checkQueue}
          onPrint={() => void print()}
          onSliceOnly={() => void sliceOnly()}
          onDownload={() => void download()}
          onSaveStl={() => void saveStl()}
        />
      {/key}
    {/snippet}
  </PlateToolbar>

  <Input
    id="{id}-model"
    type="file"
    accept={ACCEPT}
    multiple
    class="hidden"
    onchange={pick}
  />

  <div
    class="relative min-h-0 min-w-0 overflow-hidden bg-muted/50"
    role="region"
    aria-label={t('slicer.prepare.plate')}
    ondragover={(event) => {
      event.preventDefault();
    }}
    ondrop={dropped}
  >
    <canvas
      class="absolute inset-0 size-full touch-none"
      aria-label={t('slicer.prepare.view')}
      data-mode={plate.mode}
      {@attach attach}
    ></canvas>

    {#if loading || plate.parts.length === 0 || webglFailed}
      <div class="pointer-events-none absolute inset-0 grid place-items-center">
        <p
          class="grid max-w-68 place-items-center gap-2 border border-border bg-card p-4 text-center text-sm"
          aria-live="polite"
        >
          <FileUp class="size-5 text-muted-foreground" aria-hidden="true" />
          {#if loading}
            {t('slicer.prepare.reading')}
          {:else if webglFailed}
            {t('slicer.prepare.noWebGL')}
          {:else}
            {t('slicer.prepare.drop')}
          {/if}
        </p>
      </div>
    {/if}

    <div class="absolute inset-x-3 top-3 grid gap-2">
      <FaultAlert fault={destinationFault} />
      <FaultAlert fault={flowFault} />
      {#each modelFaults as fault, at (at)}
        <FaultAlert
          fault={{ code: undefined, title: fault, detail: undefined }}
        />
      {/each}
    </div>

    <div
      class="pointer-events-none absolute inset-x-3 bottom-3 flex justify-center"
      role="status"
      aria-live="polite"
    >
      {#if noticeText}
        <p
          class="border-s-2 border-primary bg-card px-3 py-1.5 text-sm text-foreground"
          data-part="plate-notice"
        >
          {noticeText}
        </p>
      {/if}
    </div>
  </div>

  <PlateStatus
    parts={plate.parts.length}
    selected={plate.selected.length}
    {selectedName}
    {bed}
    printerName={printer?.name}
    {waiting}
    {blocked}
    warning={plate.overlapping.length > 0
      ? t('slicer.plate.overlap', { count: plate.overlapping.length })
      : undefined}
    blockedId="{id}-blocked"
    {t}
  />
</div>

<style>
  .workspace {
    grid-template-rows: auto minmax(0, 1fr) auto;
    /* The workspace fills the window between the app's header and its phone
       bar; both heights are published by the app shell. */
    block-size: calc(
      100svh - var(--app-header-height, 0px) - var(--app-phone-bar-height, 0px)
    );
    min-block-size: 28rem;
  }

  canvas[data-mode='layflat'] {
    cursor: crosshair;
  }

  canvas:global([data-hover='move']) {
    cursor: grab;
  }

  canvas:global([data-hover='layflat']) {
    cursor: pointer;
  }
</style>
