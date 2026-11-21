<!--
  The tab reads the module's routes through the session the setup holds.
  The story holds one whose answers come from the fakes.
-->
<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { holdFakeSession } from '../../test/fakes';
  import PrinterQueueTab from './printer-queue-tab.svelte';

  holdFakeSession();

  const { Story } = defineMeta({
    title: 'Print queue/PrinterQueueTab',
    component: PrinterQueueTab,
    args: { deviceId: 'mk4-1' },
  });
</script>

<Story name="Live" />
