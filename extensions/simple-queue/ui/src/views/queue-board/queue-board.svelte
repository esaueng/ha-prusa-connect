<!--
  The queues of the printers a person sees, on one time axis, a lane each,
  or as a row of cards each. A printer's name opens its Queue tab, where its
  files are listed and changed. Each queue keeps its own order: the module
  plans nothing across printers.
-->
<script lang="ts">
  import {
    EmptyState,
    FaultAlert,
    ListingSkeleton,
  } from '@prusa/connect-ui/primitives';
  import PrinterIcon from '@lucide/svelte/icons/printer';

  import type { Translate } from '../../api';
  import { localized } from '../../faults';
  import type { BoardView } from '../../board-store.svelte';
  import { BoardCards } from '../board-cards';
  import { BoardTimeline } from '../board-timeline';
  import type { BoardMode } from '../../board-mode';

  let {
    view,
    mode = 'timeline',
    t,
    hrefOf,
  }: {
    view: BoardView;
    mode?: BoardMode;
    t: Translate;
    /** The Queue tab of one printer, where its files are listed and changed. */
    hrefOf: (device: string) => string;
  } = $props();
</script>

<section class="grid min-w-0 grid-cols-1 gap-4" data-part="queue-board">
  {#if view.load === 'loading'}
    <ListingSkeleton label={t('simple-queue.loading')} rows={3} />
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
  {:else if view.queues.length === 0}
    <EmptyState
      icon={PrinterIcon}
      heading={t('simple-queue.overview.empty.title')}
      headingLevel="h2"
      description={t('simple-queue.overview.empty.text')}
    />
  {:else if mode === 'cards'}
    <!-- A read that failed while the queues are on screen is said above
         them. -->
    {#if view.fault}
      <FaultAlert fault={localized(view.fault, t)} />
    {/if}
    {#if view.actionFault}
      <FaultAlert fault={localized(view.actionFault, t)} />
    {/if}
    <BoardCards
      queues={view.queues}
      {t}
      {hrefOf}
      onMove={view.move?.bind(view)}
    />
  {:else}
    {#if view.fault}
      <FaultAlert fault={localized(view.fault, t)} />
    {/if}
    <BoardTimeline queues={view.queues} {t} {hrefOf} />
  {/if}
</section>
