<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { manyBoard, someBoard, t } from '../../test/fakes';
  import BoardCards from './board-cards.svelte';

  const NOW = new Date(2026, 10, 13, 9, 35).getTime();
  const { Story } = defineMeta({
    title: 'Print queue/BoardCards',
    component: BoardCards,
    args: {
      queues: someBoard,
      t,
      hrefOf: (device: string) => `/printers/${device}`,
      now: NOW,
    },
  });
</script>

<Story name="Three printers" />
<Story name="Twenty printers" args={{ queues: manyBoard }} />
