<!--
  The Queue tab of one printer: what happens next, the file on its way or
  printing, the files that wait and the prints that ended. It draws what a
  PrinterQueueView holds, so a story hands it a view in place of the server.
  Add files opens a dialog over the printer's own files and this server's
  storages.
-->
<script lang="ts">
  import {
    Card,
    CardContent,
    FaultAlert,
    ListingSkeleton,
    toast,
  } from '@prusa/connect-ui/primitives';

  import type { FileAt } from '../../adding';
  import { localized } from '../../faults';
  import type { Translate } from '../../api';
  import type { FilePickerView } from '../../file-picker.svelte';
  import type {
    AddedFiles,
    PrinterQueueView,
  } from '../../printer-store.svelte';
  import { activeOf, waitingOf } from '../../queue-text';
  import { planOf, slotsOf } from '../../timeline';
  import { ActiveEntry } from '../active-entry';
  import { AddFiles } from '../add-files';
  import { HistoryList } from '../history-list';
  import { QueueStatus } from '../queue-status';
  import { QueueTimeline } from '../queue-timeline';
  import { WaitingList } from '../waiting-list';

  let {
    view,
    t,
    me,
    picker,
    upload,
    send,
    printerHref,
    now,
  }: {
    view: PrinterQueueView;
    t: Translate;
    /** The principal reading the page. */
    me: string | undefined;
    /** A new file picker for this printer, one for each time the dialog opens. */
    picker?: (() => FilePickerView) | undefined;
    /**
     * Sends files from the person's computer to the printer and onto its
     * queue, through the question the printer's page asks for a dropped
     * file. Undefined offers no upload.
     */
    upload?: ((files: File[]) => void) | undefined;
    /**
     * Copies files from a storage to the printer at once and puts them on its
     * queue while they are on their way, through the printer's page, which
     * asks about a name the printer holds. Undefined, or a printer that is
     * away, puts a stored file on the queue as it is, and the queue copies
     * it when its turn comes.
     */
    send?: ((files: { storage: string; path: string }[]) => void) | undefined;
    /** The printer's own Queue tab, when the queue is drawn away from it. */
    printerHref?: string | undefined;
    /** The moment the queue's plan starts from. The clock, unless a test holds it. */
    now?: number | undefined;
  } = $props();

  const active = $derived(view.queue ? activeOf(view.queue) : undefined);
  const waiting = $derived(view.queue ? waitingOf(view.queue) : []);

  // When each file prints, laid out again each time the queue is read, which
  // is every few seconds, so the plan moves with the clock. `now` is a prop
  // only so a test and a story hold the clock still.
  const plan = $derived.by(() => {
    if (!view.queue) return undefined;

    return { at: now ?? Date.now(), queue: view.queue };
  });
  const laid = $derived(plan ? planOf(plan.queue, plan.at) : undefined);
  const slots = $derived(slotsOf(laid));

  let picking = $state.raw<FilePickerView | undefined>(undefined);
  let refused = $state.raw<AddedFiles['failed']>([]);

  const openPicker = (): void => {
    if (!picker) return;
    const next = picker();
    picking = next;
    void next.start();
  };

  const closePicker = (): void => {
    picking?.dispose();
    picking = undefined;
  };

  $effect(() => closePicker);

  // A stored file goes to the printer now when the printer is there to take
  // it, so a name it already holds is settled while the person is here.
  const sends = $derived(
    send !== undefined && view.queue?.printer.connected === true
      ? send
      : undefined,
  );

  const add = async (picked: FileAt[]): Promise<void> => {
    const stored = sends
      ? picked.flatMap((file) =>
          file.storage ? [{ storage: file.storage, path: file.path }] : [],
        )
      : [];
    if (sends && stored.length > 0) sends(stored);
    const files = sends ? picked.filter((file) => !file.storage) : picked;
    if (files.length === 0) return;
    const done = await view.addFiles(files);
    refused = done.failed;
    if (done.added > 0) {
      toast.success(t('simple-queue.addFiles.done', { count: done.added }));
    }
  };
</script>

{#snippet onPrinter()}
  {#if active && view.queue}
    <ActiveEntry
      entry={active}
      device={view.queue.device_id}
      slot={slots.get(active.id)}
      now={plan?.at}
      {t}
      {me}
      progress={view.queue.progress}
      paused={view.queue.printer.paused === true}
      busy={view.busy}
      onPause={view.queue.may_change ? () => void view.pause() : undefined}
      onResume={view.queue.may_change ? () => void view.resume() : undefined}
      onStop={view.queue.may_change ? () => void view.stop() : undefined}
    />
  {/if}
{/snippet}

<div
  class="@container grid min-w-0 grid-cols-1 content-start gap-4"
  data-part="printer-queue"
>
  {#if view.load === 'loading'}
    <ListingSkeleton label={t('simple-queue.loading')} rows={4} />
  {:else if view.load === 'refused'}
    <FaultAlert
      fault={{
        code: view.fault?.code,
        title: t('simple-queue.refused'),
        detail: view.fault?.detail,
      }}
    />
  {:else if view.load === 'failed'}
    <FaultAlert fault={localized(view.fault, t)} />
  {:else if view.queue}
    {@const queue = view.queue}
    <!-- A read that failed while the queue is on screen is said above it. -->
    <FaultAlert fault={localized(view.fault, t)} />
    <FaultAlert fault={localized(view.actionFault, t)} />
    {#if refused.length > 0}
      <FaultAlert
        fault={{
          code: undefined,
          title: t('simple-queue.addFiles.failed', { count: refused.length }),
          detail: refused
            .map((one) => `${one.name}: ${localized(one.fault, t).title}`)
            .join('\n'),
        }}
      />
    {/if}
    <QueueStatus
      {queue}
      {t}
      busy={view.busy}
      onPrintNext={() => void view.printNext()}
      onReady={(ready: boolean) => void view.setReady(ready)}
      onAlone={(allowed: boolean) => void view.setAlone(allowed)}
      mayAllow={view.mayAllow}
      {printerHref}
    />
    {#if laid && plan}
      <QueueTimeline plan={laid} now={plan.at} {t} />
    {/if}
    <!-- The queue is one list with the whole width: the file on the printer
         first, then what waits. What ended is one row under it, which a
         person opens when they want to look. -->
    <div class="grid min-w-0 grid-cols-1 gap-4">
      <Card>
        <CardContent class="grid min-w-0 grid-cols-1 gap-4">
          <WaitingList
            entries={waiting}
            device={queue.device_id}
            {t}
            {me}
            mayChange={queue.may_change}
            limit={queue.limits.max_waiting}
            busy={view.busy}
            onMove={(entry: string, position: number) =>
              void view.move(entry, position)}
            onRemove={(entry: string) => void view.remove(entry)}
            onRemoveMany={async (entries: string[]) => {
              // One at a time, so each removal reads the queue the last left.
              for (const entry of entries) await view.remove(entry);
            }}
            onAddFiles={picker && queue.may_change ? openPicker : undefined}
            {slots}
            now={plan?.at}
            lead={active ? onPrinter : undefined}
          />
        </CardContent>
      </Card>
      <Card>
        <CardContent>
          <HistoryList
            entries={view.history}
            device={queue.device_id}
            {t}
            {me}
            mayChange={queue.may_change}
            busy={view.busy}
            onClear={() => void view.clearHistory()}
          />
        </CardContent>
      </Card>
    </div>
  {/if}
</div>

{#if picking && view.queue}
  <AddFiles
    open
    onOpenChange={(next: boolean) => {
      if (!next) closePicker();
    }}
    picker={picking}
    printer={view.queue.printer.name}
    {t}
    onAdd={add}
    storedLater={send !== undefined && sends === undefined}
    onUpload={upload === undefined
      ? undefined
      : (files: File[]) => {
          closePicker();
          upload(files);
        }}
  />
{/if}
