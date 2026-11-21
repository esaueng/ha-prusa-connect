<!--
  The page the host mounts at /m/<namespace>/queue: every queue the person
  sees, read in one request every few seconds for as long as the page is
  open, however many printers there are. Drawn by QueuePage.
-->
<script lang="ts">
  import { BoardStore } from '../../board-store.svelte';
  import { heldSession } from '../../session';
  import {
    BOARD_MODE_KEY,
    type BoardMode,
    isBoardMode,
  } from '../../board-mode';
  import { QueuePage } from '../queue-page';

  const { host, api } = heldSession();

  const namespace = host.namespace;
  const tabId = `${namespace}.queue`;

  const hrefOf = (device: string): string =>
    `/printers/${encodeURIComponent(device)}/${encodeURIComponent(tabId)}`;

  const board = new BoardStore(api);

  // The way the person last chose to see the queues, kept on the server for
  // them, so it follows them to another browser.
  const stored = $derived(host.store.get(BOARD_MODE_KEY));
  const mode = $derived(isBoardMode(stored) ? stored : 'timeline');

  $effect(() => {
    board.start();
    board.follow(host.client);

    return () => board.dispose();
  });
</script>

<QueuePage
  view={board}
  {mode}
  onMode={(next: BoardMode) => host.store.set(BOARD_MODE_KEY, next)}
  t={host.t}
  {hrefOf}
  referenceHref={`/docs?module=${encodeURIComponent(namespace)}`}
/>
