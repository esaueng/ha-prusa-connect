<!--
  The tab the host mounts on a printer's page: the store of that printer's
  queue, read from the module's routes for as long as the tab is open, drawn
  by PrinterQueue.
-->
<script lang="ts">
  import { FilePicker } from '../../file-picker.svelte';
  import { PrinterQueueStore } from '../../printer-store.svelte';
  import { heldSession } from '../../session';
  import { PrinterQueue } from '../printer-queue';

  let { deviceId }: { deviceId: string } = $props();

  const { host, api, grants } = heldSession();
  let store = $state.raw<PrinterQueueStore | undefined>(undefined);

  // A store for each printer, so a tab the host keeps while the printer
  // changes reads the new one.
  $effect(() => {
    const next = new PrinterQueueStore(api, grants, deviceId);
    store = next;
    next.start();
    next.follow(host.client);

    return () => {
      next.dispose();
    };
  });
</script>

{#if store}
  <PrinterQueue
    view={store}
    t={host.t}
    me={host.principal.id}
    picker={() => new FilePicker(host.client, deviceId)}
    upload={host.uploadToPrinter === undefined
      ? undefined
      : (files: File[]) => {
          // `queue` is this module's entry of the device-upload-action slot.
          host.uploadToPrinter?.(deviceId, files, 'queue');
        }}
    send={host.sendToPrinter === undefined
      ? undefined
      : (files: { storage: string; path: string }[]) => {
          host.sendToPrinter?.(deviceId, files, 'queue');
        }}
  />
{/if}
