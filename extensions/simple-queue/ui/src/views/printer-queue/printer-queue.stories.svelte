<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import {
    emptyQueue,
    fakePicker,
    heldQueue,
    longQueue,
    ME,
    onPrinterQueue,
    printingQueue,
    queueView,
    readOnlyQueue,
    refusal,
    sendingQueue,
    t,
    waitingQueue,
  } from '../../test/fakes';
  import PrinterQueue from './printer-queue.svelte';

  const { Story } = defineMeta({
    title: 'Print queue/PrinterQueue',
    component: PrinterQueue,
    args: { t, me: ME, view: queueView(printingQueue), picker: fakePicker },
  });
</script>

<Story name="Printing" />
<Story name="Loading" args={{ view: queueView(undefined, 'loading') }} />
<Story name="Failed" args={{ view: queueView(undefined, 'failed') }} />
<Story name="Refused" args={{ view: queueView(undefined, 'refused') }} />
<Story name="Empty" args={{ view: queueView(emptyQueue) }} />
<Story name="Waiting" args={{ view: queueView(waitingQueue) }} />
<Story name="File on the printer" args={{ view: queueView(onPrinterQueue) }} />
<Story name="Sending" args={{ view: queueView(sendingQueue) }} />
<Story name="Held" args={{ view: queueView(heldQueue) }} />
<Story name="Read only" args={{ view: queueView(readOnlyQueue) }} />
<Story
  name="Action failed"
  args={{ view: queueView(waitingQueue, 'ready', { actionFault: refusal }) }}
/>
<Story
  name="Phone"
  args={{ view: queueView(longQueue) }}
  globals={{ viewport: { value: 'phone', isRotated: false } }}
/>
<Story name="Dark" globals={{ theme: 'dark' }} />
