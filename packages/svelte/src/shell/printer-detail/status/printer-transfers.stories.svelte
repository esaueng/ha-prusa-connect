<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import type { SendsIndicatorRow } from '../../sends-indicator';
  import PrinterTransfers from './printer-transfers.svelte';
  import type { PrinterTransferSource } from './types';

  const { Story } = defineMeta({
    title: 'Parts/Screen components/Printer Detail/Transfers',
    component: PrinterTransfers,
  });

  const LABEL = 'text-xs font-medium text-muted-foreground';

  /** One file part way to the printer, and one whose length is not known. */
  const transfers: PrinterTransferSource = {
    inFlight: [
      {
        transferId: '01M1GRZD931RAKEXTQ7V15GC1K',
        storage: 'uploads',
        source: 'Bracket_Left_Reinforced_v4_FINAL.bgcode',
        path: 'usb/Bracket_Left_Reinforced_v4_FINAL.bgcode',
        bytesTotal: 8_400_000,
        bytesSent: 5_400_000,
        state: 'sending',
        reason: undefined,
      },
      {
        transferId: '01M1GRZD931RAKEXTQ7V15GC1M',
        storage: 'uploads',
        source: 'hinge.bgcode',
        path: 'usb/hinge.bgcode',
        bytesTotal: 0,
        bytesSent: 0,
        state: 'sending',
        reason: undefined,
      },
    ],
    stop: () => Promise.resolve(),
  };

  /** A file still going up from the browser to this server. */
  const uploads: SendsIndicatorRow[] = [
    {
      id: 'job:1',
      deviceId: 'p0',
      printerName: 'MK4S #1042',
      name: 'cube.bgcode',
      step: 'uploading',
      progress: 0.4,
      mine: true,
      startedBy: undefined,
      canCancel: true,
      stopping: false,
    },
  ];
</script>

<Story name="To the printer" asChild>
  <PrinterTransfers {transfers} label={LABEL} />
</Story>

<Story name="Going up and to the printer" asChild>
  <PrinterTransfers
    {transfers}
    {uploads}
    onCancelUpload={() => undefined}
    label={LABEL}
  />
</Story>

<Story name="At phone width" asChild>
  <div class="max-w-90">
    <PrinterTransfers {transfers} {uploads} label={LABEL} />
  </div>
</Story>
