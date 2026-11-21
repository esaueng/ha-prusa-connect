<!--
  The block the host mounts on a printer's card. Every card reads the one
  overview the session holds, so a dashboard of many printers asks the module
  once and not once for each card.
-->
<script lang="ts">
  import { heldSession } from '../../session';
  import { QueueCardLine } from '../queue-card-line';

  let { deviceId }: { deviceId: string } = $props();

  const { host, overview } = heldSession();

  $effect(() => overview.watch());
</script>

<QueueCardLine summary={overview.of(deviceId)} t={host.t} />
