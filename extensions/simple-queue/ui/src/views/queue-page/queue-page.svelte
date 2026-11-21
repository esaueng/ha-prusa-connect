<!--
  The Print queue page: the queues of the printers one under another, on a
  timeline or as cards as the person chose, and a link to this module's
  routes in the core's API reference. It draws what the
  view it is handed holds, so a story hands it a view in place of the server.
-->
<script lang="ts">
  import ExternalLinkIcon from '@lucide/svelte/icons/external-link';

  import type { Translate } from '../../api';
  import type { BoardView } from '../../board-store.svelte';
  import { QueueBoard } from '../queue-board';
  import type { BoardMode } from '../../board-mode';
  import { ViewSwitch } from '../view-switch';

  let {
    view,
    mode = 'timeline',
    onMode,
    t,
    hrefOf,
    referenceHref = '/docs?module=com.github.aux.prusa3d.simple-queue',
  }: {
    view: BoardView;
    /** How the queues are drawn. */
    mode?: BoardMode;
    /** Called with the view the person picks. Without it no choice is offered. */
    onMode?: ((mode: BoardMode) => void) | undefined;
    t: Translate;
    /** The Queue tab of one printer. */
    hrefOf: (device: string) => string;
    /** The core's reference page showing this module's routes. */
    referenceHref?: string;
  } = $props();
</script>

<div
  class="@container grid min-w-0 grid-cols-1 content-start gap-4 p-4"
  data-part="queue-page"
>
  <div class="flex flex-wrap items-center justify-between gap-2">
    <!-- The shell names the module already, so the title is for a screen reader. -->
    <h1 class="sr-only">{t('simple-queue.title')}</h1>
    {#if onMode}
      <ViewSwitch {mode} {onMode} {t} />
    {/if}
    <a
      href={referenceHref}
      target="_blank"
      rel="noopener noreferrer"
      class="ms-auto inline-flex w-fit items-center gap-1.5 text-sm underline underline-offset-4 outline-none hover:text-primary focus-visible:bg-accent"
      data-part="api-link"
    >
      {t('simple-queue.apiLink')}
      <ExternalLinkIcon aria-hidden="true" class="size-3.5" />
    </a>
  </div>
  <QueueBoard {view} {mode} {t} {hrefOf} />
</div>
