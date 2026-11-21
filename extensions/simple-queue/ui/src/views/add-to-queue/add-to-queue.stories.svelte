<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import type { Added } from '../../adding';
  import { t } from '../../test/fakes';
  import AddToQueue from './add-to-queue.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/AddToQueue',
    component: AddToQueue,
    args: {
      t,
      printers: [
        { id: 'mk4-1', name: 'MK4 by the window' },
        { id: 'core-one', name: 'CORE One' },
      ],
      onAdd: (device: string): Promise<Added> =>
        Promise.resolve(
          device === 'core-one'
            ? {
                kind: 'failed',
                fault: {
                  code: undefined,
                  title: 'The queue of this printer is full',
                  detail: undefined,
                },
              }
            : { kind: 'added', position: 4 },
        ),
    },
  });
</script>

<Story name="Two printers" />
<Story
  name="One printer"
  args={{ printers: [{ id: 'mk4-1', name: 'MK4 by the window' }] }}
/>
<Story name="Nothing chosen" args={{ printers: [] }} />
<Story
  name="Phone"
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
