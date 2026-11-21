<!--
  The file the queue sent to the printer: on its way, or printing. A print
  shows how far it is, and a person who may print on the printer pauses,
  resumes or stops it here. Stopping asks first, because a stopped print does
  not go on. A send is stopped where the printer's page shows it, and the queue
  follows what the server and the printer report: a stopped send puts the file
  first in the queue again.
-->
<script lang="ts">
  import { formatDateTime } from '@prusa/connect-ui/i18n';
  import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    Badge,
    Button,
    Progress,
  } from '@prusa/connect-ui/primitives';
  import PauseIcon from '@lucide/svelte/icons/pause';
  import PlayIcon from '@lucide/svelte/icons/play';
  import SquareIcon from '@lucide/svelte/icons/square';

  import type { Entry, Translate } from '../../api';
  import { stateBadgeVariant } from '@prusa/connect-ui/shell';
  import { activeKey, personOf } from '../../queue-text';
  import type { Slot } from '../../timeline';
  import { EntryThumbnail } from '../entry-thumbnail';
  import { EntryFacts } from '../entry-facts';
  import { EntryTimes } from '../entry-times';

  let {
    entry,
    device,
    slot,
    now = Date.now(),
    t,
    me,
    progress,
    paused = false,
    busy = false,
    onPause,
    onResume,
    onStop,
  }: {
    entry: Entry;
    /** The printer the queue is of, for the file's picture. */
    device?: string | undefined;
    /** When the file ends, from the queue's plan. */
    slot?: Slot | undefined;
    /** The moment the plan starts from. */
    now?: number | undefined;
    t: Translate;
    /** The principal reading the page, so their own name reads "you". */
    me: string | undefined;
    /** How far the print is, in whole percent, when the printer says. */
    progress?: number | undefined;
    /** The printer has the print paused. */
    paused?: boolean;
    /** An action is on its way, so the buttons wait for it. */
    busy?: boolean;
    /** Absent for a person who may not print on the printer. */
    onPause?: (() => void) | undefined;
    onResume?: (() => void) | undefined;
    onStop?: (() => void) | undefined;
  } = $props();

  let stopping = $state(false);

  const starter = $derived(personOf(entry.started_by, me));
  const startedBy = $derived.by(() => {
    if (!entry.started_by) return t('simple-queue.active.startedByQueue');
    if (starter.you) return t('simple-queue.active.startedByYou');

    return t('simple-queue.active.startedBy', { name: starter.name });
  });
</script>

<section
  class="grid min-w-0 grid-cols-1 gap-2 bg-primary/5 p-3"
  data-part="active-entry"
  data-state={entry.state}
>
  <!-- The name and the facts keep room for themselves: on a narrow screen
       the times go to a line of their own under them. -->
  <div class="flex min-w-0 flex-wrap items-start gap-3">
    <EntryThumbnail {device} {entry} name={entry.name} />
    <div class="grid min-w-0 flex-1 basis-32 grid-cols-1 gap-0.5">
      <div class="flex min-w-0 flex-wrap items-center gap-2">
        <span class="min-w-0 truncate font-medium" title={entry.path}
          >{entry.name}</span
        >
        <!-- A paused print reads as the printer says it, as the badge at
             the top of the printer's page does. -->
        {#if entry.state === 'printing' && paused}
          <Badge variant={stateBadgeVariant('PAUSED')}
            >{t('simple-queue.state.PAUSED')}</Badge
          >
        {:else}
          <Badge variant={entry.state === 'printing' ? 'stateActive' : 'info'}
            >{t(`simple-queue.entry.${activeKey(entry)}`)}</Badge
          >
        {/if}
      </div>
      <EntryFacts {entry} {t} />
      <span class="text-xs text-muted-foreground">
        {startedBy}, {t('simple-queue.active.since', {
          when: formatDateTime(entry.changed_at),
        })}
      </span>
    </div>
    <div class="ms-auto shrink-0">
      <EntryTimes {slot} {now} {t} />
    </div>
  </div>
  {#if entry.state === 'printing'}
    <div
      class="flex min-w-0 flex-wrap items-center gap-3"
      data-part="print-progress"
    >
      {#if progress !== undefined}
        <Progress
          value={progress}
          max={100}
          tone={paused ? 'muted' : 'primary'}
          class="min-w-32 flex-1"
          aria-label={t('simple-queue.active.progress', {
            percent: Math.round(progress),
          })}
        />
        <span class="text-sm tabular-nums">{Math.round(progress)} %</span>
      {:else}
        <span class="flex-1"></span>
      {/if}
      {#if onPause && onResume && onStop}
        <div class="flex shrink-0 items-center gap-2">
          {#if paused}
            <Button
              variant="secondary"
              size="sm"
              disabled={busy}
              onclick={onResume}
              data-part="resume"
            >
              <PlayIcon aria-hidden="true" class="size-4" />
              {t('simple-queue.active.resume')}
            </Button>
          {:else}
            <Button
              variant="secondary"
              size="sm"
              disabled={busy}
              onclick={onPause}
              data-part="pause"
            >
              <PauseIcon aria-hidden="true" class="size-4" />
              {t('simple-queue.active.pause')}
            </Button>
          {/if}
          <Button
            variant="destructive"
            size="sm"
            disabled={busy}
            onclick={() => (stopping = true)}
            data-part="stop"
          >
            <SquareIcon aria-hidden="true" class="size-4" />
            {t('simple-queue.active.stop')}
          </Button>
        </div>
      {/if}
    </div>
  {/if}
  {#if entry.state === 'sending'}
    <div class="flex flex-wrap items-center gap-2 text-sm">
      <p class="min-w-0 flex-1 text-muted-foreground">
        {t(
          entry.storage === ''
            ? 'simple-queue.active.openingNote'
            : 'simple-queue.active.sendingNote',
        )}
      </p>
    </div>
  {/if}
</section>

<AlertDialog bind:open={stopping}>
  <AlertDialogContent>
    <AlertDialogHeader>
      <AlertDialogTitle>{t('simple-queue.active.stopAsk')}</AlertDialogTitle>
      <AlertDialogDescription>
        {t('simple-queue.active.stopNote', { name: entry.name })}
      </AlertDialogDescription>
    </AlertDialogHeader>
    <AlertDialogFooter>
      <AlertDialogCancel
        >{t('simple-queue.active.keepPrinting')}</AlertDialogCancel
      >
      <AlertDialogAction
        variant="destructive"
        onclick={() => {
          stopping = false;
          onStop?.();
        }}
      >
        {t('simple-queue.active.stopConfirm')}
      </AlertDialogAction>
    </AlertDialogFooter>
  </AlertDialogContent>
</AlertDialog>
