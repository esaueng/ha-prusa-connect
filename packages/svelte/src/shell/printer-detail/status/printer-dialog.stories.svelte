<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { capabilityFor, LIFECYCLE_VERBS, type Device } from '../../../domain';
  import { fakeDevice } from '../../../test/fakes';
  import type { TelemetrySource } from '../types';
  import PrinterDialog from './printer-dialog.svelte';

  const { Story } = defineMeta({
    title: 'Parts/Screen components/Printer Detail/Printer Dialog',
    component: PrinterDialog,
  });

  const telemetry: TelemetrySource = {
    value: () => undefined,
    points: () => [],
  };

  const commands = { onCommand: () => undefined };

  /** A printer asking a question on its own screen, which a person may answer. */
  const asking = (
    state: Record<string, unknown>,
    actions = ['observe', 'operate'],
  ): Device => ({
    ...fakeDevice('p0', 'MK4S #1042'),
    model: 'MK4S',
    actions,
    capabilities: [
      ...LIFECYCLE_VERBS.map(capabilityFor),
      'core.command.dialog_action',
    ],
    state: { '/state': 'ATTENTION', ...state },
  });
</script>

<!-- The question as the firmware words it, with its buttons. -->
<Story name="Asks a question" asChild>
  <PrinterDialog
    device={asking({
      '/dialog/id': 7,
      '/dialog/title': 'Filament runout',
      '/dialog/text': 'Insert filament, then press Retry.',
      '/dialog/buttons': ['Retry', 'Cancel'],
    })}
    {telemetry}
    {commands}
  />
</Story>

<!-- The firmware sent a code and no words, and the catalogue names the code. -->
<Story name="Named by its code" asChild>
  <PrinterDialog
    device={asking({
      '/dialog/id': 11,
      '/dialog/code': '13999',
      '/dialog/buttons': ['Yes', 'No', 'Purge_more'],
    })}
    {telemetry}
    {commands}
  />
</Story>

<!-- A person who may only watch reads the question and answers it on the printer. -->
<Story name="Read only" asChild>
  <PrinterDialog
    device={asking(
      {
        '/dialog/id': 7,
        '/dialog/title': 'Filament runout',
        '/dialog/text': 'Insert filament, then press Retry.',
        '/dialog/buttons': ['Retry', 'Cancel'],
      },
      ['observe'],
    )}
    {telemetry}
    {commands}
  />
</Story>
