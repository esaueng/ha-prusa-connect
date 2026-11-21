<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import type { Entry, Queue } from '../../api';
  import { printingQueue, t, waitingQueue } from '../../test/fakes';
  import { planOf } from '../../timeline';
  import QueueTimeline from './queue-timeline.svelte';

  const NOW = new Date(2026, 10, 13, 9, 35).getTime();

  const timed = (queue: Queue, seconds: readonly number[]): Queue => {
    let waiting = 0;

    return {
      ...queue,
      entries: queue.entries.map((entry): Entry => {
        if (entry.state !== 'waiting') return entry;
        const estimated = seconds[waiting++] ?? 0;

        return estimated > 0
          ? { ...entry, estimated_seconds: estimated }
          : entry;
      }),
    };
  };

  const planFor = (queue: Queue) => {
    const plan = planOf(queue, NOW);
    if (plan === undefined) throw new Error('the story needs a queue of files');

    return plan;
  };

  const { Story } = defineMeta({
    title: 'Print queue/QueueTimeline',
    component: QueueTimeline,
    args: {
      t,
      now: NOW,
      plan: planFor(timed(waitingQueue, [7500, 3600, 14220])),
    },
  });
</script>

<Story name="Waits for a person" />
<Story
  name="A print runs"
  args={{
    plan: planFor({
      ...timed(printingQueue, [7500, 3600]),
      remaining_seconds: 4020,
    }),
  }}
/>
<Story
  name="A file of unknown length"
  args={{ plan: planFor(timed(waitingQueue, [7500, 0, 14220])) }}
/>
<Story
  name="Two days"
  args={{ plan: planFor(timed(waitingQueue, [90000, 60000, 14220])) }}
/>
<Story
  name="Phone"
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
<Story name="Dark" globals={{ theme: 'dark' }} />
