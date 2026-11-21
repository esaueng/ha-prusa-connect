<!-- Three tabs, the last of which has no panel, for the detail-tabs tests. -->
<script lang="ts">
  import DetailTabs from '../primitives/detail-tabs/detail-tabs.svelte';

  let {
    active,
    onChange = () => undefined,
    extra = false,
    loadsPage = false,
  }: {
    active: string;
    onChange?: (id: string) => void;
    extra?: boolean;
    loadsPage?: boolean;
  } = $props();
</script>

{#snippet status()}<div data-testid="status-panel">Status</div>{/snippet}
{#snippet files()}<div data-testid="files-panel">Files</div>{/snippet}

<DetailTabs
  {active}
  {onChange}
  tabs={[
    { id: 'status', label: 'Status', panel: status },
    { id: 'files', label: 'Files', panel: files },
    { id: 'info', label: 'Info', loadsPage },
    ...(extra ? [{ id: 'late', label: 'Late module' }] : []),
  ]}
/>
