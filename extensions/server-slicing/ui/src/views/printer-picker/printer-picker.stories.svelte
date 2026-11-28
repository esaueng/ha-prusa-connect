<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { printerChoicesOf } from '../../plate/printers';
  import { t } from '../../test/fakes';
  import {
    charlie,
    henry,
    printer,
    xl,
  } from '../prepare-panel/prepare-fixtures';
  import PrinterPicker from './printer-picker.svelte';

  const printers = printerChoicesOf([
    henry,
    charlie,
    xl,
    printer('olga', 'Olga', 'IDLE', { connected: false }),
  ]);

  const { Story } = defineMeta({
    title: 'Server slicing/PrinterPicker',
    component: PrinterPicker,
    args: { printers, chosen: 'henry', t, onChoose: () => undefined },
  });
</script>

<Story name="Idle printer" />
<Story name="Printing printer" args={{ chosen: 'charlie' }} />
<Story name="Offline printer" args={{ chosen: 'olga' }} />
<Story name="No printers" args={{ printers: [], chosen: undefined }} />
