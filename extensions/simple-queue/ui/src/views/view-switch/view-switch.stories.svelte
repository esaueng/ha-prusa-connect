<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t } from '../../test/fakes';
  import ViewSwitch from './view-switch.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/ViewSwitch',
    component: ViewSwitch,
    args: { mode: 'timeline', onMode: () => undefined, t },
  });
</script>

<Story name="Timeline" />
<Story name="Cards" args={{ mode: 'cards' }} />
