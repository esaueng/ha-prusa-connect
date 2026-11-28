<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t } from '../../test/fakes';
  import PrintButton from './print-button.svelte';

  const none = (): void => undefined;

  const { Story } = defineMeta({
    title: 'Server slicing/PrintButton',
    component: PrintButton,
    args: {
      decision: { action: 'print', block: undefined },
      estimate: undefined,
      step: 'idle',
      canSlice: true,
      canSave: true,
      t,
      onPrint: none,
      onSliceOnly: none,
      onDownload: none,
      onSaveStl: none,
    },
  });
</script>

<Story name="Print" />
<Story name="Print with the time" args={{ estimate: 12_300 }} />
<Story name="Slicing" args={{ step: 'slicing' }} />
<Story name="Sending" args={{ step: 'sending', estimate: 12_300 }} />
<Story
  name="Queue unavailable"
  args={{ decision: { action: 'queue', block: 'noQueue' } }}
/>
<Story
  name="Nothing to print"
  args={{
    decision: { action: 'print', block: 'empty' },
    canSlice: false,
    canSave: false,
  }}
/>
