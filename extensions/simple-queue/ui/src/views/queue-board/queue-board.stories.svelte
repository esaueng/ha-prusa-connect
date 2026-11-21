<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { boardView, failure, manyBoard, refusal, t } from '../../test/fakes';
  import QueueBoard from './queue-board.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/QueueBoard',
    component: QueueBoard,
    args: {
      t,
      hrefOf: (device: string) => `/printers/${device}`,
      view: boardView(),
    },
  });
</script>

<Story name="All printers" />
<Story name="Twenty printers" args={{ view: boardView(manyBoard) }} />
<Story name="Loading" args={{ view: boardView([], 'loading') }} />
<Story name="Failed" args={{ view: boardView([], 'failed', failure) }} />
<Story name="Refused" args={{ view: boardView([], 'refused', refusal) }} />
<Story name="Empty" args={{ view: boardView([]) }} />
<Story
  name="Phone"
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
<Story
  name="Phone, twenty printers"
  args={{ view: boardView(manyBoard) }}
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
<Story name="Dark" globals={{ theme: 'dark' }} />
