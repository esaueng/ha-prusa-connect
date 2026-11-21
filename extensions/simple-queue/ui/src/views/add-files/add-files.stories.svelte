<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import type { FilePickerView } from '../../file-picker.svelte';
  import { fakePicker, t } from '../../test/fakes';
  import AddFiles from './add-files.svelte';

  const still = (fields: Partial<FilePickerView>): FilePickerView => ({
    ...fakePicker(),
    places: [{ kind: 'printer' }],
    place: { kind: 'printer' },
    directory: 'usb',
    root: 'usb',
    rows: [],
    view: 'folders',
    partial: false,
    load: 'ready',
    fault: undefined,
    picked: [],
    start: () => Promise.resolve(),
    choose: () => Promise.resolve(),
    show: () => Promise.resolve(),
    open: () => Promise.resolve(),
    isPicked: () => false,
    toggle: () => undefined,
    dispose: () => undefined,
    ...fields,
  });

  const { Story } = defineMeta({
    title: 'Print queue/AddFiles',
    component: AddFiles,
    args: {
      open: true,
      onOpenChange: () => undefined,
      printer: 'MK4 by the window',
      t,
      onAdd: () => Promise.resolve(),
    },
  });
</script>

<Story name="The printer's USB drive" args={{ picker: fakePicker() }} />
<Story name="Loading" args={{ picker: still({ load: 'loading' }) }} />
<Story
  name="Failed"
  args={{
    picker: still({
      load: 'failed',
      fault: {
        code: 'PRINTER_UNREACHABLE',
        title: 'The printer did not answer',
        detail: 'Check that it is on and connected.',
      },
    }),
  }}
/>
<Story name="Empty" args={{ picker: still({}) }} />
<Story
  name="Phone"
  args={{ picker: fakePicker() }}
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
