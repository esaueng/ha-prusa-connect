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
  name="Assembly from 3MF"
  args={{
    source: 'local:assembled.3mf',
    api: { ...testPrepare, read: assembledFile },
  }}
/>
<Story
  name="Import limit for 3MF"
  args={{
    source: 'local:large-archive.3mf',
    api: { ...testPrepare, read: () => loadFixture('large-archive.3mf') },
  }}
/>
<Story
  name="Unknown part roles in 3MF"
  args={{
    source: 'local:bambu-unknown-role.3mf',
    api: { ...testPrepare, read: () => loadFixture('bambu-unknown-role.3mf') },
  }}
/>
<Story name="No saved profile" args={{ beds: {} }} />
