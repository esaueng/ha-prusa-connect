<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { printingEntry, t, waitingQueue } from '../../test/fakes';
  import type { Slot } from '../../timeline';
  import EntryTimes from './entry-times.svelte';

  const NOW = new Date(2026, 10, 13, 9, 35).getTime();
  const HOUR = 60 * 60 * 1000;
  const waiting = waitingQueue.entries.find(
    (entry) => entry.state === 'waiting',
  );
  if (waiting === undefined)
    throw new Error('the fake queue has a waiting file');

  const slot: Slot = {
    entry: waiting,
    active: false,
    start: NOW + HOUR,
    end: NOW + 3 * HOUR,
    known: true,
    firm: true,
  };

  const { Story } = defineMeta({
    title: 'Print queue/EntryTimes',
    component: EntryTimes,
    args: { t, slot, now: NOW },
  });
</script>

<Story name="A waiting file" />
<Story
  name="Behind a file of unknown length"
  args={{ slot: { ...slot, firm: false } }}
/>
<Story name="Length unknown" args={{ slot: { ...slot, known: false } }} />
<Story
  name="On the printer"
  args={{ slot: { ...slot, entry: printingEntry, active: true, start: NOW } }}
/>
<Story name="No slot" args={{ slot: undefined }} />
<Story name="Dark" globals={{ theme: 'dark' }} />
