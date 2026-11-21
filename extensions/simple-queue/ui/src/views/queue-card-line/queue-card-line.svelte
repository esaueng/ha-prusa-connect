<!--
  One line on a printer's card: what the queue sends, prints or has paused,
  and how many files wait. A printer whose queue holds nothing gets no line, so the card
  stays as it is.
-->
<script lang="ts">
  import CalendarClockIcon from '@lucide/svelte/icons/calendar-clock';

  import type { Summary, Translate } from '../../api';

  let { summary, t }: { summary: Summary | undefined; t: Translate } = $props();

  const line = $derived.by((): string | undefined => {
    if (summary === undefined) return undefined;
    const { active, next, waiting } = summary;
    if (active?.state === 'sending') {
      return t('simple-queue.card.sending', { name: active.name });
    }
    if (active) {
      // A paused print reads as paused, as the printer's own badge says.
      const doing = summary.printer.paused === true ? 'paused' : 'printing';

      return waiting > 0
        ? t(`simple-queue.card.${doing}AndWaiting`, {
            name: active.name,
            count: waiting,
          })
        : t(`simple-queue.card.${doing}`, { name: active.name });
    }
    if (next) {
      return t('simple-queue.card.next', { count: waiting, name: next.name });
    }

    return undefined;
  });
</script>

{#if line !== undefined}
  <span
    class="flex min-w-0 items-center gap-1.5 text-sm text-muted-foreground"
    data-part="queue-card-line"
  >
    <CalendarClockIcon aria-hidden="true" class="size-3.5 shrink-0" />
    <span class="min-w-0 truncate">{line}</span>
  </span>
{/if}
