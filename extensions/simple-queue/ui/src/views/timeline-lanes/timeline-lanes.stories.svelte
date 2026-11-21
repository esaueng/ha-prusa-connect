<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { printingQueue, t } from '../../test/fakes';
  import { planOf } from '../../timeline';
  import TimelineLanes from './timeline-lanes.svelte';

  const NOW = new Date(2026, 10, 13, 9, 35).getTime();
  const plan = planOf(printingQueue, NOW);
  const lanes = [{ key: 'mk4-1', slots: plan?.slots ?? [] }];

  const { Story } = defineMeta({
    title: 'Print queue/TimelineLanes',
    component: TimelineLanes,
    args: { lanes, end: plan?.end ?? NOW, now: NOW, hours: 3, t },
  });
</script>

<Story name="Three hours a mark" />
<Story name="One hour a mark" args={{ hours: 1 }} />
