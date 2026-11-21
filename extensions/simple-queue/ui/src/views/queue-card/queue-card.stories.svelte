<!--
  The card line reads the overview the session holds. The story holds one
  whose answers come from the fakes.
-->
<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { holdFakeSession } from '../../test/fakes';
  import QueueCard from './queue-card.svelte';

  holdFakeSession();

  const { Story } = defineMeta({
    title: 'Print queue/QueueCard',
    component: QueueCard,
    args: { deviceId: 'mk4-1' },
  });
</script>

<Story name="Live" />
