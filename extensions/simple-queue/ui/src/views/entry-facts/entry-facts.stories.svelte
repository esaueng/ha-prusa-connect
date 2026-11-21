<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t, waitingQueue } from '../../test/fakes';
  import EntryFacts from './entry-facts.svelte';

  const waiting = waitingQueue.entries[0];
  if (waiting === undefined) throw new Error('the fake queue has a file');

  const entry = {
    ...waiting,
    printer_model: 'XL2IS',
    layer_height_mm: 0.2,
    material: 'PLA;PETG',
    size: 4_760_000,
  };
  const sizeOnly = { ...waiting, size: 3_220_000 };

  const { Story } = defineMeta({
    title: 'Print queue/EntryFacts',
    component: EntryFacts,
    args: { t, entry },
  });
</script>

<Story name="Every fact" />
<Story name="Only the size" args={{ entry: sizeOnly }} />
