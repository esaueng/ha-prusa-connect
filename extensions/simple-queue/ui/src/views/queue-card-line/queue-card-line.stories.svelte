<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { someQueues, t } from '../../test/fakes';
  import QueueCardLine from './queue-card-line.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/QueueCardLine',
    component: QueueCardLine,
    args: { t, summary: someQueues.printers[0] },
  });
</script>

<Story name="Printing" />
<Story name="Next" args={{ summary: someQueues.printers[1] }} />
<Story name="Nothing" args={{ summary: someQueues.printers[2] }} />
