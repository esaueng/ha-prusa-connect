<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import type { PrintTarget } from '../../print-picker';
  import DropChoice from './drop-choice.svelte';

  const { Story } = defineMeta({
    title: 'Parts/Screen components/Printer Detail/Drop Choice',
    component: DropChoice,
  });

  const free: PrintTarget = {
    id: 'p1',
    model: 'MK4',
    name: 'MK4 by the window',
    ready: true,
    reasonKey: undefined,
    reasonStates: undefined,
    state: 'IDLE',
  };

  const printing: PrintTarget = {
    ...free,
    ready: false,
    reasonKey: 'printPicker.reason.state.PRINTING',
    state: 'PRINTING',
  };

  const handlers = {
    onConfirm: () => undefined,
    onCancel: () => undefined,
  };
</script>

<!-- One file on a printer that can start it: the dialog starts on Print now. -->
<Story
  name="One File On A Free Printer"
  args={{
    names: ['Bracket_Left_Reinforced_v4_FINAL.bgcode'],
    printer: 'MK4 by the window',
    printTarget: free,
    ...handlers,
  }}
/>

<!-- The printer is busy, so the file can only be copied, and it says why. -->
<Story
  name="The Printer Is Printing"
  args={{
    names: ['bracket.bgcode'],
    printer: 'MK4 by the window',
    printTarget: printing,
    ...handlers,
  }}
/>

<!-- Several files: one of them is printed and goes first, the rest are
     copied. The person picks which. -->
<Story
  name="Several Files"
  args={{
    names: [
      'Bracket_Left_Reinforced_v4_0.4n_0.2mm_PETG_MK4S_1h12m.bgcode',
      'Bracket_Right_Reinforced_v4_0.4n_0.2mm_PETG_MK4S_1h12m.bgcode',
      'Cable_Clip_x10_0.4n_0.2mm_PLA_MK4S_24m.bgcode',
    ],
    printer: 'MK4 by the window',
    printTarget: free,
    ...handlers,
  }}
/>

<!-- Several files on a busy printer are only copied, and it says why. -->
<Story
  name="Several Files On A Printing Printer"
  args={{
    names: ['bracket.bgcode', 'hinge.bgcode', 'lid.bgcode'],
    printer: 'MK4 by the window',
    printTarget: printing,
    ...handlers,
  }}
/>
