<!--
  The block the host mounts in the print dialog. It reads which printers are
  ticked from the dialog and adds the file to their queues through the
  module's routes. A file the printer cannot print gets no block.
-->
<script lang="ts">
  import { parseFileRef, printDialogContext } from '@prusa/connect-ui/slots';

  import { addTo } from '../../adding';
  import { isPrintable } from '../../queue-text';
  import { heldSession } from '../../session';
  import { AddToQueue } from '../add-to-queue';

  let { fileRef }: { fileRef: string } = $props();

  const { host, api } = heldSession();
  const dialog = printDialogContext();

  const file = $derived(parseFileRef(fileRef));
  const printers = $derived(
    dialog.selected.map((id) => ({
      id,
      name: host.fleet.device(id)?.name ?? id,
    })),
  );
</script>

{#if file && isPrintable(file.path)}
  {@const at = file}
  <AddToQueue
    {printers}
    t={host.t}
    onAdd={(device: string) => addTo(api, device, at)}
  />
{/if}
