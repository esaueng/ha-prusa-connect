<!--
  The route reads the overview the session holds. The story holds one whose
  answers come from the fakes.
-->
<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { holdFakeSession } from '../../test/fakes';
  import QueueRoute from './queue-route.svelte';

  holdFakeSession();

  const { Story } = defineMeta({
    title: 'Print queue/QueueRoute',
    component: QueueRoute,
  });
</script>

<Story name="Live" />
