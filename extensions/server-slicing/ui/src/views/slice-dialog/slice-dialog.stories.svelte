<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t } from '../../test/fakes';
  import SliceDialog from './slice-dialog.svelte';

  const { Story } = defineMeta({
    title: 'Server slicing/SliceDialog',
    component: SliceDialog,
    args: {
      t,
      submit: () => Promise.resolve('j1'),
      onClose: () => undefined,
      onSliced: () => undefined,
    },
  });
</script>

<Story name="A project" args={{ project: 'local:projekty/box.3mf' }} />
<Story
  name="STL with saved profiles"
  args={{
    project: 'local:models/box.stl',
    profiles: ['core-one-petg', 'mk4s-pla'],
  }}
/>
<Story name="STL with no profiles" args={{ project: 'local:models/box.stl' }} />
<Story name="No file named" args={{ project: 'not a reference' }} />
