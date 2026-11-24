<!--
  The page the host mounts at /m/<namespace>/slicer: the store, read from the
  module's routes for as long as the page is open, and the address the page
  keeps its place in, drawn by SlicerPage.
-->
<script lang="ts">
  import type { ListingStore } from '@prusa/connect-ui/primitives';

  import { PageAddress } from '../../page-address.svelte';
  import { heldSession } from '../../session';
  import { SlicerStore } from '../../store.svelte';
  import { prepareOn } from '../prepare-panel/prepare-api';
  import { SlicerPage } from '../slicer-page';

  const { host, api } = heldSession();
  const store = new SlicerStore(api);
  const prepare = prepareOn(host.client, api);
  const address = new PageAddress(window);

  // The listings remember their columns, view and page size in the module's
  // own store, so they come back the way the person left them.
  const remembered: ListingStore = {
    read: (key) => host.store.get(key) ?? null,
    write: (key, value) => {
      host.store.set(key, value);
    },
  };

  // The API tab shows the address this page was served from, which is the one
  // a machine on the same network reaches the server at. The namespace is the
  // host's to choose, so it is read out of the page's own address.
  const namespace = host.pageHref('slicer').split('/')[2] ?? '';
  const coreBase = `${window.location.origin}/api/v1`;
  const apiBase = `${coreBase}/modules/${namespace}`;

  $effect(() => {
    store.start();
    const detach = address.attach();

    return () => {
      detach();
      store.dispose();
    };
  });
</script>

<SlicerPage
  view={store}
  t={host.t}
  {address}
  store={remembered}
  {apiBase}
  {coreBase}
  {prepare}
  tokensHref="/settings/tokens"
  referenceHref={`/docs?module=${encodeURIComponent(namespace)}`}
/>
