<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { ME, printingEntry, sendingEntry, t } from '../../test/fakes';
  import ActiveEntry from './active-entry.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/ActiveEntry',
    component: ActiveEntry,
    args: {
      t,
      me: ME,
      entry: printingEntry,
    },
  });
</script>

<Story name="Printing" />
<Story name="Sending" args={{ entry: sendingEntry }} />
<Story
  name="Phone"
  args={{ entry: sendingEntry }}
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
<Story name="Dark" globals={{ theme: 'dark' }} />
