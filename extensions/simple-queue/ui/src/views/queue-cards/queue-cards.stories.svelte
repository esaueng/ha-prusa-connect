<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { emptyQueue, printingQueue, t } from '../../test/fakes';
  import { planOf, slotsOf } from '../../timeline';
  import QueueCards from './queue-cards.svelte';

  const NOW = new Date(2026, 10, 13, 9, 35).getTime();
  const { Story } = defineMeta({
    title: 'Print queue/QueueCards',
    component: QueueCards,
    args: {
      queue: printingQueue,
      slots: slotsOf(planOf(printingQueue, NOW)),
      now: NOW,
      t,
      onMove: () => undefined,
    },
  });
</script>

<Story name="Printing, three waiting" />
<Story
  name="Empty"
  args={{ queue: emptyQueue, slots: slotsOf(planOf(emptyQueue, NOW)) }}
/>
