<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { printingHistory, t } from '../../test/fakes';
  import HistoryList from './history-list.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/HistoryList',
    component: HistoryList,
    args: {
      t,
      mayChange: true,
      entries: printingHistory,
      onClear: () => undefined,
    },
  });
</script>

<Story name="Closed" />
<Story name="Open" args={{ open: true }} />
<Story name="Empty" args={{ entries: [] }} />
<Story name="Read only" args={{ mayChange: false, open: true }} />
<Story
  name="Phone"
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
<Story name="Dark" globals={{ theme: 'dark' }} />
