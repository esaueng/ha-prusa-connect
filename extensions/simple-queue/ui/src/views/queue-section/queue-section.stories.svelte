<!--
  Outside a print dialog nothing is ticked, so the section asks for a printer.
  The story holds a session whose answers come from the fakes.
-->
<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { holdFakeSession } from '../../test/fakes';
  import QueueSection from './queue-section.svelte';

  holdFakeSession();

  const { Story } = defineMeta({
    title: 'Print queue/QueueSection',
    component: QueueSection,
    args: { fileRef: 'local:parts/bracket.bgcode' },
  });
</script>

<Story name="Outside a dialog" />
