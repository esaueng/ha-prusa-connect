<!--
  Every state the build-plate workspace draws. Omitted Loading: the page draws
  the workspace only once the slicers have been read.
-->
<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';
  import { expect, userEvent, waitFor, within } from 'storybook/test';

  import { t } from '../../test/fakes';
  import type { QueueBlock } from '../../plate/printers';
  import type { PrepareApi } from './prepare-api';
  import {
    assembledFile,
    bed,
    charlie,
    henry,
    loadFixture,
    memory,
    profileSettings,
    slicedJob,
    testPrepare,
    xl,
  } from './prepare-fixtures';
  import PreparePanel from './prepare-panel.svelte';

  const { Story } = defineMeta({
    title: 'Server slicing/PreparePanel',
    component: PreparePanel,
    args: {
      api: testPrepare,
      beds: { 'core-one-petg': bed },
      profileSettings,
      devices: [henry, charlie, xl],
      remember: memory(),
      hasSlicer: true,
      links: [
        { href: '?tab=slicers', label: 'Slicers' },
        { href: '?tab=jobs', label: 'Jobs' },
        { href: '?tab=api', label: 'API' },
      ],
      t,
    },
  });

  const onCharlie = memory();
  onCharlie.write('plate.printer', 'charlie');

  const queueStory = (
    block: QueueBlock | undefined,
    enqueue: PrepareApi['enqueue'] = testPrepare.enqueue,
  ) => ({
    source: 'local:bracket.stl',
    remember: onCharlie,
    api: {
      ...testPrepare,
      enqueue,
      submit: () => Promise.resolve(slicedJob),
      checkQueue: () =>
        block === 'queueChecking'
          ? new Promise<QueueBlock>(() => undefined)
          : Promise.resolve(block),
    },
  });

  const phone = { viewport: { value: 'phone', isRotated: false } };

  const enqueue = async ({ canvasElement }: { canvasElement: HTMLElement }) => {
    const button = await within(canvasElement).findByRole('button', {
      name: 'Add to queue',
    });
    await waitFor(() => expect(button).toBeEnabled());
    await userEvent.click(button);
  };
</script>

<Story name="Empty build plate" />
<Story name="Model from Storage" args={{ source: 'local:bracket.stl' }} />
<Story name="Add to queue" args={queueStory(undefined)} />
<Story name="Checking queue" args={queueStory('queueChecking')} />
<Story name="Queue module missing" args={queueStory('queueInstall')} />
<Story name="Queue module stopped" args={queueStory('queueOffline')} />
<Story
  name="Queue permission not approved"
  args={queueStory('queueApproval')}
/>
<Story name="Queue permission denied" args={queueStory('queueDenied')} />
<Story name="Queue printer missing" args={queueStory('queuePrinterMissing')} />
<Story
  name="Queue printer unreadable"
  args={queueStory('queuePrinterUnknown')}
/>
<Story name="Queue unavailable" args={queueStory('queueUnavailable')} />
<Story
  name="Queued at a position"
  args={queueStory(undefined)}
  play={enqueue}
/>
<Story
  name="Queued without a position"
  args={queueStory(undefined, () => Promise.resolve({ waiting: 1 }))}
  play={enqueue}
/>
<Story
  name="Queue full"
  args={queueStory(undefined, () =>
    Promise.reject(
      Object.assign(new Error('the queue of this printer is full'), {
        code: 'server-slicing/PRINT_QUEUE_FULL',
      }),
    ),
  )}
  play={enqueue}
/>
<Story
  name="Assembly from 3MF"
  args={{
    source: 'local:assembled.3mf',
    api: { ...testPrepare, read: assembledFile },
  }}
/>
<Story
  name="Import limit for 3MF"
  args={{
    source: 'local:large-archive.3mf',
    api: { ...testPrepare, read: () => loadFixture('large-archive.3mf') },
  }}
/>
<Story
  name="Unknown part roles in 3MF"
  args={{
    source: 'local:bambu-unknown-role.3mf',
    api: { ...testPrepare, read: () => loadFixture('bambu-unknown-role.3mf') },
  }}
/>
<Story name="No saved profile" args={{ beds: {}, profileSettings: {} }} />
<Story name="No PrusaSlicer 2" args={{ hasSlicer: false }} />
<Story name="No printers" args={{ devices: [] }} />
<Story name="Phone" args={{ source: 'local:bracket.stl' }} globals={phone} />
