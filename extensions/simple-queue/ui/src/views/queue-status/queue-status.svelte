<!--
  The top of a printer's Queue tab: one sentence about what happens next, and
  the one or two things a person does about it. Away from the printer's own
  page it also names the printer and its state.
  The printer starts a file only when somebody said the bed is clear: with
  the start button here, or on the printer's own screen when the switch
  lets the queue start the next file by itself.
  A printer with a ready state always shows a pair of buttons, the one that
  sets it ready and Unset ready, and exactly one of the two is live.
-->
<script lang="ts">
  import {
    Alert,
    AlertDescription,
    Badge,
    Button,
    Card,
    CardContent,
    Label,
    Switch,
  } from '@prusa/connect-ui/primitives';
  import PlayIcon from '@lucide/svelte/icons/play';

  import type { Queue, Translate } from '../../api';
  import {
    activeOf,
    offersOf,
    outlookOf,
    printerState,
    waitingOf,
  } from '../../queue-text';
  import { stateBadgeVariant } from '@prusa/connect-ui/shell';

  let {
    queue,
    t,
    busy = false,
    onPrintNext,
    onReady,
    onAlone,
    mayAllow = true,
    printerHref,
  }: {
    queue: Queue;
    t: Translate;
    /** An action is on its way, so the buttons wait for it. */
    busy?: boolean;
    onPrintNext: () => void;
    /** True says the bed is clear, false takes that back. */
    onReady: (ready: boolean) => void;
    /** Switches whether the queue may print when the printer is marked ready. */
    onAlone: (allowed: boolean) => void;
    /** False greys the switch out: the person does not administer the printer. */
    mayAllow?: boolean;
    /** The printer's own Queue tab, when the status is drawn away from it. */
    printerHref?: string | undefined;
  } = $props();

  const uid = $props.id();
  const state = $derived(printerState(queue.printer));
  const offers = $derived(offersOf(queue));
  const outlook = $derived(outlookOf(queue));
  const name = $derived((activeOf(queue) ?? waitingOf(queue)[0])?.name ?? '');
  // The next file waits to be started, by the person or, on a printer marked
  // ready, by the queue. A file a note holds is here too, with a hint that
  // sends the person to the note, and its button stays live.
  const upNext = $derived(
    offers.printNext &&
      (outlook === 'markBed' ||
        outlook === 'pressPrint' ||
        outlook === 'byHand' ||
        outlook === 'starting' ||
        outlook === 'held'),
  );
  // A file on its way or printing has a card of its own right under this one,
  // with its name and what it is doing, so nothing is said about it here.
  const active = $derived(activeOf(queue) !== undefined);
  // Without the sentence the card may have nothing left to show, and is then
  // not drawn: a printer with no ready state has no switch either.
  const shown = $derived(
    !active ||
      printerHref !== undefined ||
      offers.allowAlone ||
      !queue.may_change,
  );
  // On a printer already marked ready the clear bed is confirmed, so with
  // the switch on the queue starts the file and the button waits for it.
  // Two cases leave the button to the person, because nothing else would
  // start the file: the file carries a note that holds it, or the switch is
  // off.
  const held = $derived((waitingOf(queue)[0]?.note ?? '') !== '');
  const byPerson = $derived(held || !queue.starts_alone);
  const waitsForQueue = $derived(upNext && offers.readyNow && !byPerson);
  const hint = $derived.by(() => {
    if (held) {
      return offers.readyNow
        ? 'simple-queue.next.readyHeld'
        : 'simple-queue.next.held';
    }
    if (!offers.readyNow) return 'simple-queue.next.hint';
    if (queue.starts_alone) return 'simple-queue.next.readyOn';

    return mayAllow
      ? 'simple-queue.next.readyOff'
      : 'simple-queue.next.readyPress';
  });
  // A ready printer, a file waiting and nothing to start it: the switch is
  // off and this person may turn it on. That is said in a box and the switch
  // is marked, so it is not read as the queue being stuck.
  const nudge = $derived(upNext && hint === 'simple-queue.next.readyOff');
  // Only a printer with a ready state has a clear bed to confirm, and one
  // marked ready has it confirmed already, so its button only prints.
  const startLabel = $derived(
    queue.printer.can_be_ready && !offers.readyNow
      ? 'simple-queue.action.readyAndPrint'
      : 'simple-queue.action.printNext',
  );
</script>

{#if shown}
  <Card data-part="queue-status">
    <CardContent class="grid min-w-0 grid-cols-1 gap-3">
      <!-- A printer's own page already shows its name and state above the
         tabs, so they are drawn only where this is shown away from it. -->
      {#if printerHref}
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <h2 class="min-w-0 truncate text-base font-semibold">
            <a
              href={printerHref}
              class="rounded-sm underline-offset-4 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
              >{queue.printer.name}</a
            >
          </h2>
          <Badge variant={stateBadgeVariant(state)} data-part="printer-state"
            >{t(`simple-queue.state.${state}`)}</Badge
          >
        </div>
      {/if}
      {#if !active}
        <div class="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-3">
          {#if upNext}
            <!-- A file waits: its name is a fact of its own, not part of a
             sentence, so a long name wraps without breaking one. The
             buttons stay beside it while the name keeps a column of its own,
             and go under it on a narrow screen. -->
            <div
              class="grid min-w-0 flex-1 basis-48 grid-cols-1 gap-1"
              data-part="outlook"
            >
              <span
                class="text-xs font-semibold tracking-wide text-muted-foreground uppercase"
                >{t('simple-queue.next.label')}</span
              >
              <span class="min-w-0 font-semibold break-words">{name}</span>
              {#if !nudge}
                <span class="text-sm text-muted-foreground">{t(hint)}</span>
              {/if}
            </div>
          {:else}
            <p class="min-w-0 flex-1 basis-48 text-sm" data-part="outlook">
              {t(`simple-queue.outlook.${outlook}`, { name })}
            </p>
          {/if}
          <!-- On a phone the buttons fill the width, side by side when they
               fit and one a line when they do not. -->
          <div class="flex w-full flex-wrap items-center gap-2 sm:w-auto">
            {#if offers.printNext}
              <!-- A file whose last start failed keeps the button live on a
               ready printer too: the queue does not try it again by itself. -->
              <Button
                onclick={onPrintNext}
                disabled={busy || waitsForQueue}
                title={waitsForQueue ? t(hint) : undefined}
                class="grow sm:grow-0"
                data-part="print-next"
              >
                <PlayIcon aria-hidden="true" class="size-4" />
                {t(startLabel)}
              </Button>
            {/if}
            {#if offers.markReady}
              <Button
                onclick={() => onReady(true)}
                disabled={busy || offers.readyNow}
                class="grow sm:grow-0"
                data-part="mark-ready"
              >
                {t('simple-queue.action.markReady')}
              </Button>
            {/if}
            {#if offers.unmarkReady}
              <Button
                variant="secondary"
                onclick={() => onReady(false)}
                disabled={busy || !offers.readyNow}
                class="grow sm:grow-0"
                data-part="unmark-ready"
              >
                {t('simple-queue.action.unmarkReady')}
              </Button>
            {/if}
          </div>
        </div>
      {/if}
      {#if nudge}
        <div data-part="alone-off">
          <Alert variant="warning">
            <AlertDescription>{t(hint)}</AlertDescription>
          </Alert>
        </div>
      {/if}
      {#if offers.allowAlone}
        <!-- A setting and not an action: nothing prints when it is switched.
           It lets the queue start a file when no person is pressing Print:
           after the bed is marked clear at the printer, and when a file that
           was added on its way lands. -->
        <Label
          variant="option"
          class={[
            'min-w-0 items-center gap-3 rounded-md border p-3',
            nudge ? 'border-warning' : 'border-border',
          ]}
          title={mayAllow ? undefined : t('simple-queue.alone.notYours')}
          data-part="alone"
        >
          <Switch
            checked={queue.starts_alone}
            disabled={busy || !mayAllow}
            onCheckedChange={onAlone}
            aria-label={t('simple-queue.action.allowAlone')}
            aria-describedby={`${uid}-alone-hint`}
          />
          <span class="flex min-w-0 flex-col gap-0.5">
            <span>{t('simple-queue.action.allowAlone')}</span>
            <span
              id={`${uid}-alone-hint`}
              class="text-sm text-muted-foreground"
            >
              {t('simple-queue.alone.hint')}
            </span>
          </span>
        </Label>
      {/if}
      {#if !queue.may_change}
        <p class="text-sm text-muted-foreground" data-part="read-only">
          {t('simple-queue.readOnly')}
        </p>
      {/if}
    </CardContent>
  </Card>
{/if}
