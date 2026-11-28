<!--
  Every state the build-plate workspace draws. Omitted Loading: the page draws
  the workspace only once the slicers have been read.
-->
<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t } from '../../test/fakes';
  import {
    assembledFile,
    bed,
    charlie,
    henry,
    loadFixture,
    memory,
    profileSettings,
    testPrepare,
    xl,
  } from './prepare-fixtures';
  import PreparePanel from './prepare-panel.svelte';

  const { Story } = defineMeta({
    title: 'Server slicing/PreparePanel',
    component: PreparePanel,
    args: {
      api: testPrepare,
      beds: { 'core-one-petg': bed },
      profileSettings,
      devices: [henry, charlie, xl],
      remember: memory(),
      hasSlicer: true,
      t,
    },
  });

  const onCharlie = memory();
  onCharlie.write('plate.printer', 'charlie');

  const phone = { viewport: { value: 'phone', isRotated: false } };
</script>

<Story name="Empty build plate" />
<Story name="Model from Storage" args={{ source: 'local:bracket.stl' }} />
<Story
  name="Busy printer"
  args={{ source: 'local:bracket.stl', remember: onCharlie }}
/>
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
<Story name="No saved profile" args={{ beds: {}, profileSettings: {} }} />
<Story name="No PrusaSlicer 2" args={{ hasSlicer: false }} />
<Story name="No printers" args={{ devices: [] }} />
<Story name="Phone" args={{ source: 'local:bracket.stl' }} globals={phone} />
