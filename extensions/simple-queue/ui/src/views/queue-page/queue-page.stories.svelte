<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { boardView, t } from '../../test/fakes';
  import QueuePage from './queue-page.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/QueuePage',
    component: QueuePage,
    args: {
      t,
      hrefOf: (device: string) => `/printers/${device}`,
      view: boardView(),
    },
  });
</script>

<Story name="Queues" />
<Story
  name="Phone"
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
