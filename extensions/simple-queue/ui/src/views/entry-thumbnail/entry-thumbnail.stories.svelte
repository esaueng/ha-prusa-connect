<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import EntryThumbnail from './entry-thumbnail.svelte';

  // A story has no server to ask for a picture, so each one draws the glyph a
  // file without a picture gets. The picture itself is the library's
  // FileThumbnail, which has its own stories.
  const { Story } = defineMeta({
    title: 'Print queue/EntryThumbnail',
    component: EntryThumbnail,
    args: {
      device: 'mk4-1',
      entry: { storage: 'local', path: 'parts/bracket.bgcode' },
      name: 'bracket.bgcode',
    },
  });
</script>

<Story name="A file in a storage" />
<Story
  name="A file on the printer"
  args={{ entry: { storage: '', path: 'usb/bracket.bgcode' } }}
/>
<Story name="No printer named" args={{ device: undefined }} />
<Story name="Dark" globals={{ theme: 'dark' }} />
