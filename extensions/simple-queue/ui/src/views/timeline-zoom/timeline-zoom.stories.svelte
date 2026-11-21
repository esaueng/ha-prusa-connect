<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t } from '../../test/fakes';
  import TimelineZoom from './timeline-zoom.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/TimelineZoom',
    component: TimelineZoom,
    args: { hours: 3, t },
  });
</script>

<Story name="Three hours a mark" />
<Story name="Closest" args={{ hours: 1 }} />
