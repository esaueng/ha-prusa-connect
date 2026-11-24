<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t } from '../../test/fakes';
  import { bed, testPrepare } from './prepare-fixtures';
  import PreparePanel from './prepare-panel.svelte';

  const { Story } = defineMeta({
    title: 'Server slicing/PreparePanel',
    component: PreparePanel,
    args: {
      api: testPrepare,
      beds: { 'core-one-petg': bed },
      hasSlicer: true,
      onSliced: () => undefined,
      t,
    },
  });
</script>

<Story name="Empty build plate" />
<Story name="STL from Storage" args={{ source: 'local:bracket.stl' }} />
<Story name="No saved profile" args={{ beds: {} }} />
