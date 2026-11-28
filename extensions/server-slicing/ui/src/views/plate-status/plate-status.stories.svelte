<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { t } from '../../test/fakes';
  import { bed } from '../prepare-panel/prepare-fixtures';
  import PlateStatus from './plate-status.svelte';

  const { Story } = defineMeta({
    title: 'Server slicing/PlateStatus',
    component: PlateStatus,
    args: {
      parts: 3,
      selected: 0,
      selectedName: undefined,
      bed,
      printerName: 'Henry',
      blocked: undefined,
      blockedId: 'blocked',
      t,
    },
  });
</script>

<Story name="Parts" />
<Story
  name="One selected"
  args={{ selected: 1, selectedName: 'bracket.stl' }}
/>
<Story name="Several selected" args={{ selected: 2 }} />
<Story name="Prints waiting" args={{ waiting: 2 }} />
<Story
  name="Blocked"
  args={{ blocked: 'A part is outside the print volume.' }}
/>
<Story
  name="Empty"
  args={{ parts: 0, bed: undefined, printerName: undefined }}
/>
