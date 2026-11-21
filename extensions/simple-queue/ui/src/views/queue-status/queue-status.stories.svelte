<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import {
    awayQueue,
    emptyQueue,
    heldQueue,
    manualQueue,
    printingQueue,
    readOnlyQueue,
    readyQueue,
    waitingQueue,
    t,
  } from '../../test/fakes';
  import QueueStatus from './queue-status.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/QueueStatus',
    component: QueueStatus,
    args: {
      t,
      queue: waitingQueue,
      onPrintNext: () => undefined,
      onReady: () => undefined,
      onAlone: () => undefined,
    },
  });
</script>

<Story name="Mark the bed" />
<Story name="Starting" args={{ queue: readyQueue }} />
<Story name="Printing" args={{ queue: printingQueue }} />
<Story
  name="Ready, printing on ready not allowed"
  args={{ queue: { ...readyQueue, starts_alone: false } }}
/>
<Story name="Switch not yours" args={{ mayAllow: false }} />
<Story name="Empty" args={{ queue: emptyQueue }} />
<Story
  name="Empty and ready"
  args={{ queue: { ...readyQueue, entries: [] } }}
/>
<Story
  name="No ready state"
  args={{
    queue: {
      ...waitingQueue,
      printer: { ...waitingQueue.printer, can_be_ready: false },
    },
  }}
/>
<Story name="Press print" args={{ queue: manualQueue }} />
<Story name="Held" args={{ queue: heldQueue }} />
<Story name="Away" args={{ queue: awayQueue }} />
<Story name="Read only" args={{ queue: readOnlyQueue }} />
<Story name="Busy" args={{ busy: true }} />
<Story
  name="Phone"
  args={{ queue: manualQueue }}
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
<Story name="Dark" globals={{ theme: 'dark' }} />
