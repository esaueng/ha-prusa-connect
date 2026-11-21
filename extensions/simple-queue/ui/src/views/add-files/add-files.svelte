<!--
  The Add files dialog of a printer's Queue tab. A person picks the place the
  files are in, the printer itself or a storage of this server, opens folders
  and ticks print files, and puts all of them on the queue at once. The files
  go on in the order they were selected.
-->
<script lang="ts">
  import { formatBytes, formatDateTime } from '@prusa/connect-ui/i18n';
  import {
    Button,
    Checkbox,
    Dialog,
    DialogContent,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DIALOG_BODY,
    DIALOG_SCROLLS,
    EmptyState,
    FaultAlert,
    FileDropzone,
    FolderTrail,
    Label,
    ListingNoMatch,
    ListingSearch,
    ListingSkeleton,
    ToggleGroup,
    ToggleGroupItem,
  } from '@prusa/connect-ui/primitives';
  import FolderIcon from '@lucide/svelte/icons/folder';
  import FolderOpenIcon from '@lucide/svelte/icons/folder-open';

  import type { FileAt } from '../../adding';
  import type { Translate } from '../../api';
  import type { FilePickerView, PickerView } from '../../file-picker.svelte';
  import { localized } from '../../faults';
  import { keyOf, type Place, trailOf } from '../../files';
  import { PRINTABLE } from '../../queue-text';
  import { EntryThumbnail } from '../entry-thumbnail';

  let {
    open,
    onOpenChange,
    picker,
    printer,
    t,
    onAdd,
    onUpload,
    storedLater = false,
  }: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    picker: FilePickerView;
    /** The printer's name, for the title. */
    printer: string;
    t: Translate;
    /** Puts the ticked files on the queue, and closes the dialog once it answered. */
    onAdd: (files: FileAt[]) => Promise<void>;
    /**
     * Sends files from the person's computer to the printer and onto its
     * queue. Undefined offers no upload.
     */
    onUpload?: ((files: File[]) => void) | undefined;
    /** A stored file is copied to the printer only when its turn comes. */
    storedLater?: boolean | undefined;
  } = $props();

  let adding = $state(false);
  const uid = $props.id();

  // A server with one storage calls it Storage: its name, such as local, says
  // nothing to a person who never set it up. Several storages are told apart
  // by their names.
  const storages = $derived(
    picker.places.filter((place) => place.kind === 'storage').length,
  );
  const titleOf = (place: Place): string => {
    if (place.kind === 'printer') return t('simple-queue.addFiles.printer');

    return storages > 1
      ? t('simple-queue.addFiles.storageNamed', { name: place.title })
      : t('simple-queue.addFiles.storage');
  };

  // The steps below the folder the place opens on.
  const trail = $derived(
    trailOf(picker.directory).filter(
      (step) => step.path.length > picker.root.length,
    ),
  );

  // The search narrows what is listed: every file of the place, or the open
  // folder. Another place, folder or view starts with the whole list again.
  let query = $state('');
  let escapeEmpties = false;
  const needle = $derived(query.trim().toLowerCase());
  const rows = $derived(
    needle === ''
      ? picker.rows
      : picker.rows.filter((row) => row.name.toLowerCase().includes(needle)),
  );
  const go = (move: Promise<void>): void => {
    query = '';
    void move;
  };

  // The folder of a file in the list of every file, below the folder the
  // place opens on. A file at the top has none to name.
  const whereOf = (where: string | undefined): string =>
    (where ?? '').slice(picker.root.length).replace(/^\//, '');

  const add = async (): Promise<void> => {
    adding = true;
    try {
      await onAdd(picker.picked.map((one) => one.file));
      onOpenChange(false);
    } finally {
      adding = false;
    }
  };
</script>

<Dialog {open} {onOpenChange}>
  <!-- One height whatever the place or folder holds, so moving between the
       printer and a storage does not resize the dialog under the pointer.
       The list scrolls inside it. An inline style, because the module's
       utilities do not reach a dialog. -->
  <DialogContent
    class={`sm:max-w-2xl ${DIALOG_SCROLLS}`}
    style="height: min(46rem, calc(100dvh - 2rem))"
  >
    <!-- A long printer name wraps, and the room on the right keeps it from
         running under the button that closes the dialog. The room is an
         inline style because a dialog is drawn outside the module's own
         element, where the module's utilities do not reach
         (styling-a-module.md), so a class of its own would be a class with
         no rule. -->
    <DialogHeader style="padding-inline-end: 2.5rem">
      <DialogTitle>{t('simple-queue.addFiles.title', { printer })}</DialogTitle>
    </DialogHeader>

    <!-- The part that scrolls cuts what is drawn outside it, so it is a
         little wider than its content: the focus ring of the search box and
         of a button at its edge stays whole. The scroll bar has a gutter of
         its own, so it does not lie over the list. -->
    <div
      data-slot="dialog-body"
      class={['flex min-w-0 flex-col gap-3', DIALOG_BODY]}
      style="margin: -0.25rem; padding: 0.25rem; scrollbar-gutter: stable"
      data-part="add-files"
    >
      {#if picker.places.length > 1}
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          class="flex-wrap justify-start"
          aria-label={t('simple-queue.addFiles.from')}
          value={keyOf(picker.place)}
          onValueChange={(key: string) => {
            const place = picker.places.find((one) => keyOf(one) === key);
            if (place) go(picker.choose(place));
          }}
        >
          {#each picker.places as place (keyOf(place))}
            <ToggleGroupItem value={keyOf(place)}>
              {titleOf(place)}
            </ToggleGroupItem>
          {/each}
        </ToggleGroup>
      {/if}

      <div class="flex min-w-0 flex-wrap items-center gap-2">
        <!-- Escape empties the box before it closes the dialog: the key
             that found text is stopped here, after the box has emptied. -->
        <div
          style="flex: 1 1 5rem; min-width: 5rem"
          role="presentation"
          onkeydowncapture={(event: KeyboardEvent) => {
            escapeEmpties =
              event.key === 'Escape' &&
              query !== '' &&
              event.target instanceof HTMLInputElement;
          }}
          onkeydown={(event: KeyboardEvent) => {
            if (escapeEmpties) event.stopPropagation();
          }}
        >
          <ListingSearch
            value={query}
            onValue={(next: string) => {
              query = next;
            }}
            label={t('simple-queue.addFiles.search')}
            placeholder={t('simple-queue.addFiles.search')}
          />
        </div>
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          aria-label={t('simple-queue.addFiles.view')}
          value={picker.view}
          onValueChange={(view: string) => {
            if (view === 'all' || view === 'folders') {
              go(picker.show(view satisfies PickerView));
            }
          }}
        >
          <ToggleGroupItem value="all">
            {t('simple-queue.addFiles.view.all')}
          </ToggleGroupItem>
          <ToggleGroupItem value="folders">
            {t('simple-queue.addFiles.view.folders')}
          </ToggleGroupItem>
        </ToggleGroup>
      </div>

      {#if onUpload}
        <!-- A file from the person's computer goes the way a file dropped on
             the printer's page goes, with Add to queue picked there. -->
        <FileDropzone
          compact
          accept={PRINTABLE.join(',')}
          label={t('simple-queue.addFiles.upload')}
          onFiles={onUpload}
          data-part="upload"
        />
      {/if}

      {#if storedLater && picker.place.kind === 'storage'}
        <p class="text-sm text-muted-foreground" data-part="stored-later">
          {t('simple-queue.addFiles.storedLater')}
        </p>
      {/if}
      <!-- At the top of a place the trail would only repeat the place picked
           above it, so it shows once a folder is open, as the way back. -->
      {#if picker.view === 'folders' && trail.length > 0}
        <FolderTrail
          crumbs={[
            { label: titleOf(picker.place), path: picker.root },
            ...trail.map((step) => ({ label: step.name, path: step.path })),
          ]}
          label={t('simple-queue.addFiles.trail')}
          onOpen={(path: string) => go(picker.open(path))}
        />
      {/if}

      {#if picker.load === 'loading'}
        <ListingSkeleton label={t('simple-queue.addFiles.loading')} rows={4} />
      {:else if picker.load === 'failed'}
        <FaultAlert fault={localized(picker.fault, t)} />
      {:else if picker.rows.length > 0 && rows.length === 0}
        <ListingNoMatch
          heading={t('simple-queue.addFiles.noMatch')}
          headingLevel="h4"
          onClear={() => {
            query = '';
          }}
          class="rounded-md border border-dashed border-border py-8"
        />
      {:else if picker.rows.length === 0}
        <EmptyState
          icon={FolderOpenIcon}
          heading={t('simple-queue.addFiles.empty.title')}
          headingLevel="h4"
          description={t('simple-queue.addFiles.empty.text')}
          class="rounded-md border border-dashed border-border py-8"
        />
      {:else}
        <ul
          id={uid}
          class="grid min-w-0 grid-cols-1 divide-y divide-border rounded-md border border-border"
        >
          {#each rows as row (row.path)}
            {@const id = `${uid}-${row.path}`}
            <li
              class="flex min-w-0 items-center gap-3 px-3 py-2 hover:bg-accent"
            >
              {#if row.folder}
                <FolderIcon
                  aria-hidden="true"
                  class="size-4 shrink-0 text-muted-foreground"
                />
                <Button
                  variant="ghost"
                  class="h-auto min-w-0 flex-1 justify-start truncate p-0 text-sm font-normal text-foreground hover:bg-transparent"
                  onclick={() => {
                    go(picker.open(row.path));
                  }}
                >
                  {row.name}
                </Button>
              {:else}
                <Checkbox
                  {id}
                  checked={picker.isPicked(row)}
                  onCheckedChange={() => {
                    picker.toggle(row);
                  }}
                />
                <EntryThumbnail
                  device={picker.device}
                  entry={{
                    storage:
                      picker.place.kind === 'printer'
                        ? ''
                        : picker.place.storage,
                    path: row.path,
                    size: row.size,
                    modified: row.modified,
                  }}
                  name={row.name}
                />
                <Label
                  for={id}
                  class="flex min-w-0 flex-1 flex-col items-start gap-0 font-normal"
                >
                  <!-- The column keeps each line to its own width, so a long
                       name is held to the row's width here to be cut short.
                       An inline style, because the module's utilities do not
                       reach a dialog. -->
                  <span class="truncate text-sm" style="max-width: 100%"
                    >{row.name}</span
                  >
                  <span class="text-xs text-muted-foreground tabular-nums">
                    {#if whereOf(row.where)}
                      {whereOf(row.where)} ·
                    {/if}
                    {formatBytes(row.size)}
                    {#if row.modified}
                      · {formatDateTime(row.modified)}
                    {/if}
                  </span>
                </Label>
              {/if}
            </li>
          {/each}
        </ul>
        {#if picker.view === 'all' && picker.partial}
          <p class="text-xs text-muted-foreground">
            {t('simple-queue.addFiles.partial')}
          </p>
        {/if}
      {/if}
    </div>

    <DialogFooter class="items-center">
      <!-- At the far side from the buttons. An inline style, for the same
           reason as the header's: the module's own utility has no rule in a
           dialog. -->
      <span
        class="text-sm text-muted-foreground"
        style="margin-inline-end: auto"
        aria-live="polite"
      >
        {t('simple-queue.addFiles.picked', { count: picker.picked.length })}
      </span>
      <Button
        variant="secondary"
        onclick={() => {
          onOpenChange(false);
        }}
      >
        {t('primitives.cancel')}
      </Button>
      <Button
        disabled={adding || picker.picked.length === 0}
        aria-busy={adding}
        onclick={() => void add()}
      >
        {t('simple-queue.addFiles.add', { count: picker.picked.length })}
      </Button>
    </DialogFooter>
  </DialogContent>
</Dialog>
