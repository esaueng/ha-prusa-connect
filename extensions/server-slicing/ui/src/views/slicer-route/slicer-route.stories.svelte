<!--
  The route reads the module's routes through the session the setup holds.
  The story holds one whose answers come from the fakes, so the route draws
  what the page draws on a running server.
-->
<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';
  import type { ModuleHost } from '@prusa/connect-ui/slots';

  import type { SlicerApi } from '../../api';
  import { holdSession } from '../../session';
  import { everybodysJobs, someSlicers, t } from '../../test/fakes';
  import { charlie, henry } from '../prepare-panel/prepare-fixtures';
  import SlicerRoute from './slicer-route.svelte';

  const api: SlicerApi = {
    listSlicers: () => Promise.resolve(someSlicers),
    listJobs: () => Promise.resolve(everybodysJobs),
    installSlicer: () => Promise.resolve(someSlicers),
    refreshSlicers: () => Promise.resolve(someSlicers),
    removeSlicer: () => Promise.resolve(undefined),
    pinSlicer: () => Promise.resolve(someSlicers),
    cancelJob: () => Promise.reject(new Error('the story cancels nothing')),
    submitJob: () => Promise.reject(new Error('the story slices nothing')),
    readJob: () => Promise.reject(new Error('the story slices nothing')),
  };

  // The route reads t, signal, store, fleet, client and pageHref of the host
  // and nothing else. The client is never called before a person acts.
  const held: Record<string, string> = {};
  holdSession({
    host: {
      t,
      signal: new AbortController().signal,
      pageHref: (path: string) =>
        `/m/com.github.aux.prusa3d.server-slicing/${path}`,
      store: {
        get: (key: string) => held[key],
        set: (key: string, value: string) => {
          held[key] = value;
        },
      },
      fleet: { devices: [henry, charlie], loading: false, loaded: true },
    } as unknown as ModuleHost,
    api,
  });

  const { Story } = defineMeta({
    title: 'Server slicing/SlicerRoute',
    component: SlicerRoute,
  });
</script>

<Story name="Live" />
