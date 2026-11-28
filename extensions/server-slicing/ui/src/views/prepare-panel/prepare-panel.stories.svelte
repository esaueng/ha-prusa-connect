<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t } from '../../test/fakes';
  import {
    assembledFile,
    bed,
    loadFixture,
    testPrepare,
  } from './prepare-fixtures';
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
<Story
  name="3MF assembly"
  args={{
    source: 'local:assembled.3mf',
    api: { ...testPrepare, read: assembledFile },
  }}
/>
<Story
  name="3MF exceeds import limits"
  args={{
    source: 'local:large-archive.3mf',
    api: { ...testPrepare, read: () => loadFixture('large-archive.3mf') },
  }}
/>
<Story name="No saved profile" args={{ beds: {} }} />
