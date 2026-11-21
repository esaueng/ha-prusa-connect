<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { heldQueue, longQueue, ME, t, waitingQueue } from '../../test/fakes';
  import WaitingList from './waiting-list.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/WaitingList',
    component: WaitingList,
    args: {
      t,
      me: ME,
      mayChange: true,
      limit: 30,
      entries: waitingQueue.entries,
      onMove: () => undefined,
      onRemove: () => undefined,
    },
  });
</script>

<Story name="Waiting" />
<Story name="Empty" args={{ entries: [] }} />
<Story name="Notes" args={{ entries: heldQueue.entries }} />
<Story name="Read only" args={{ mayChange: false }} />
<Story name="Long names" args={{ entries: longQueue.entries }} />
<Story
  name="Phone"
  args={{ entries: longQueue.entries }}
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
<Story name="Dark" globals={{ theme: 'dark' }} />
