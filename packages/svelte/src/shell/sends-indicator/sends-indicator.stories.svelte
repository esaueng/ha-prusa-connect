<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import SendsIndicator from './sends-indicator.svelte';
  import type { SendsIndicatorRow, SendsSource } from './types';

  const source = (rows: SendsIndicatorRow[]): SendsSource => ({
    rows,
    active: rows.length > 0,
    uploading: rows.some((row) => row.step === 'uploading'),
    cancel: () => undefined,
  });

  // A file this person started, and one somebody else did.
  const mine = {
    mine: true,
    startedBy: undefined,
    canCancel: true,
    stopping: false,
  };
  const theirs = {
    mine: false,
    startedBy: undefined,
    canCancel: false,
    stopping: false,
  };

  const { Story } = defineMeta({
    title: 'Shell/SendsIndicator',
    component: SendsIndicator,
  });
</script>

<Story name="Empty" args={{ sends: source([]) }} />

<Story
  name="Uploading"
  args={{
    sends: source([
      {
        id: 'job:1',
        deviceId: 'mk4',
        printerName: 'MK4 #1',
        name: 'cube.bgcode',
        step: 'uploading',
        progress: 0.4,
        ...mine,
      },
    ]),
  }}
/>

<Story
  name="Waiting For The Printer"
  args={{
    sends: source([
      {
        id: 'job:2',
        deviceId: 'mk4',
        printerName: 'MK4 #1',
        name: 'benchy.bgcode',
        step: 'waiting',
        progress: null,
        ...mine,
      },
      {
        id: 'job:1',
        deviceId: 'mk4',
        printerName: 'MK4 #1',
        name: 'cube.bgcode',
        step: 'sending',
        progress: null,
        ...mine,
        canCancel: false,
      },
    ]),
  }}
/>

<!-- A file whose name the printer holds, while the person is asked. -->
<Story
  name="Waiting For An Answer"
  args={{
    sends: source([
      {
        id: 'job:3',
        deviceId: 'mk4',
        printerName: 'MK4 #1',
        name: 'benchy.bgcode',
        step: 'asking',
        progress: null,
        ...mine,
        canCancel: false,
      },
    ]),
  }}
/>

<Story
  name="Several Files"
  args={{
    sends: source([
      {
        id: 'job:3',
        deviceId: 'mk4',
        printerName: 'MK4 #1',
        name: 'benchy.bgcode',
        step: 'sending',
        progress: 0.75,
        ...mine,
      },
      {
        id: 'job:2',
        deviceId: 'xl',
        printerName: 'XL #2',
        name: 'calibration-cube-with-a-rather-long-name.bgcode',
        step: 'uploading',
        progress: 0.2,
        ...mine,
      },
      {
        id: 'job:1',
        deviceId: 'mini',
        printerName: 'Mini #3',
        name: 'bracket.bgcode',
        step: 'sending',
        progress: null,
        ...mine,
        canCancel: false,
      },
    ]),
  }}
/>

<Story
  name="Others' Files"
  args={{
    sends: source([
      {
        id: 'transfer:t-2',
        deviceId: 'xl',
        printerName: 'XL #2',
        name: 'gearbox.bgcode',
        step: 'sending',
        progress: 0.6,
        ...theirs,
        startedBy: 'user:ada',
      },
      {
        id: 'transfer:t-1',
        deviceId: 'mini',
        printerName: 'Mini #3',
        name: 'clip.bgcode',
        step: 'sending',
        progress: 0.1,
        ...theirs,
      },
    ]),
  }}
/>

<Story
  name="Mixed"
  args={{
    sends: source([
      {
        id: 'job:4',
        deviceId: 'mk4',
        printerName: 'MK4 #1',
        name: 'benchy.bgcode',
        step: 'uploading',
        progress: 0.3,
        ...mine,
      },
      {
        id: 'transfer:t-3',
        deviceId: 'mk4',
        printerName: 'MK4 #1',
        name: 'spool-holder.bgcode',
        step: 'sending',
        progress: 0.8,
        ...mine,
      },
      {
        id: 'transfer:t-2',
        deviceId: 'xl',
        printerName: 'XL #2',
        name: 'gearbox.bgcode',
        step: 'sending',
        progress: 0.6,
        ...theirs,
        startedBy: 'user:ada',
        canCancel: true,
      },
    ]),
  }}
/>

<Story
  name="Cannot Stop"
  args={{
    sends: source([
      {
        id: 'transfer:t-5',
        deviceId: 'xl',
        printerName: 'XL #2',
        name: 'gearbox.bgcode',
        step: 'sending',
        progress: 0.45,
        ...theirs,
      },
    ]),
  }}
/>

<Story
  name="Stopping"
  args={{
    sends: source([
      {
        id: 'transfer:t-6',
        deviceId: 'xl',
        printerName: 'XL #2',
        name: 'gearbox.bgcode',
        step: 'sending',
        progress: 0.45,
        ...theirs,
        canCancel: true,
        stopping: true,
      },
    ]),
  }}
/>
