<!--
  The Slicing page: the PrusaSlicer versions on one tab and the jobs on the
  other, each a listing like the app's others, and a detail for one version or
  one job. What is on screen is kept in the address, so a link, a reload and
  the back button land where the person was. It draws what a SlicerView holds,
  so a story hands it a view in place of the server.
-->
<script lang="ts">
  import {
    DetailTabs,
    FaultAlert,
    ListingSkeleton,
    type ListingStore,
    PageHeader,
  } from '@prusa/connect-ui/primitives';

  import type { SubmitBody, Translate } from '../../api';
  import { PageAddress } from '../../page-address.svelte';
  import type { Tab } from '../../page-state';
  import { ReleaseActs } from '../../release-acts.svelte';
  import { releaseRowsOf } from '../../releases';
  import type { SlicerView } from '../../store.svelte';
  import { ApiReference } from '../api-reference';
  import { JobDetail } from '../job-detail';
  import { JobListing } from '../job-listing';
  import { ReleaseDetail } from '../release-detail';
  import { ReleaseDialog } from '../release-dialog';
  import { ReleaseListing } from '../release-listing';
  import { SliceDialog } from '../slice-dialog';

  let {
    view,
    t,
    address = new PageAddress(null),
    store = memoryStore(),
    apiBase = 'http://printers.local:8080/api/v1/modules/com.github.aux.prusa3d.server-slicing',
    coreBase = 'http://printers.local:8080/api/v1',
    tokensHref = '/settings/tokens',
    referenceHref = '/docs?module=com.github.aux.prusa3d.server-slicing',
  }: {
    view: SlicerView;
    t: Translate;
    /** Where the page is. A story hands one in a state of its own. */
    address?: PageAddress;
    /** Where a listing remembers its columns, view and page size. */
    store?: ListingStore;
    /** The module's routes on this server, for the API tab. */
    apiBase?: string;
    /** The core's API on this server. */
    coreBase?: string;
    /** Where a person mints an API credential. */
    tokensHref?: string;
    /** The core's reference page showing this module's document. */
    referenceHref?: string;
  } = $props();

  // A plain function declared below is hoisted, so the default above may name it.
  function memoryStore(): ListingStore {
    const held: Record<string, string> = {};
    return {
      read: (key) => held[key] ?? null,
      write: (key, value) => {
        held[key] = value;
      },
    };
  }

  const acts = $derived(new ReleaseActs(view));
  const state = $derived(address.state);

  const active = $derived(
    view.jobs?.jobs.filter(
      (job) => job.state === 'queued' || job.state === 'running',
    ).length ?? 0,
  );

  const releaseRow = $derived(
    state.release === undefined || view.slicers === undefined
      ? undefined
      : releaseRowsOf(view.slicers).find(
          (row) => row.version === state.release,
        ),
  );

  const openJob = $derived(
    state.job === undefined
      ? undefined
      : view.jobs?.jobs.find((job) => job.id === state.job),
  );
</script>

<!-- A plain click on a link to another state of this page is taken here. -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div
  class="@container grid min-w-0 grid-cols-1 content-start gap-4 p-4"
  data-part="slicer-page"
  onclick={address.take}
>
  {#if state.release === undefined && state.job === undefined}
    <PageHeader title={t('slicer.title')} />
  {/if}
  {#if view.load === 'loading'}
    <ListingSkeleton label={t('slicer.loading')} rows={6} />
  {:else if view.load === 'refused'}
    <FaultAlert
      fault={{
        code: view.fault?.code,
        title: t('slicer.refused'),
        detail: view.fault?.detail,
      }}
    />
  {:else if view.load === 'failed'}
    <FaultAlert fault={view.fault} />
  {:else if view.slicers && view.jobs}
    {@const slicers = view.slicers}
    {@const jobs = view.jobs}
    {#snippet slicersPanel()}
      <ReleaseListing
        {slicers}
        administrator={jobs.all}
        {address}
        {acts}
        {store}
        {t}
        onCheck={() => void view.checkReleases()}
        onInstallUpdate={(version: string) => void view.install(version, false)}
      />
    {/snippet}
    {#snippet jobsPanel()}
      <JobListing
        {jobs}
        {address}
        {store}
        {t}
        onCancel={(id: string) => void view.cancel(id)}
      />
    {/snippet}
    {#snippet apiPanel()}
      <ApiReference {apiBase} {coreBase} {tokensHref} {referenceHref} {t} />
    {/snippet}
    {#if state.release !== undefined}
      <FaultAlert fault={view.actionFault} />
      <ReleaseDetail
        version={state.release}
        row={releaseRow}
        platform={slicers.platform}
        administrator={jobs.all}
        {acts}
        backHref={address.href({ tab: 'slicers', release: undefined })}
        {t}
      />
    {:else if state.job !== undefined}
      <FaultAlert fault={view.actionFault} />
      <JobDetail
        id={state.job}
        job={openJob}
        backHref={address.href({ tab: 'jobs', job: undefined })}
        onCancel={(id: string) => void view.cancel(id)}
        {t}
      />
    {:else}
      <FaultAlert fault={view.actionFault} />
      <DetailTabs
        tabs={[
          {
            id: 'slicers',
            label: t('slicer.tab.slicers'),
            panel: slicersPanel,
          },
          {
            id: 'jobs',
            label: t('slicer.tab.jobs'),
            count: active > 0 ? active : undefined,
            panel: jobsPanel,
          },
          { id: 'api', label: t('slicer.tab.api'), panel: apiPanel },
        ]}
        active={state.tab}
        onChange={(next: string) => address.go({ tab: next as Tab })}
      />
    {/if}
  {/if}
</div>

<ReleaseDialog {acts} {t} />

{#if state.slice !== undefined}
  <SliceDialog
    project={state.slice}
    profiles={view.slicers?.stl_profiles}
    {t}
    submit={(body: SubmitBody) => view.submit(body)}
    fault={view.actionFault}
    onClose={() => {
      address.update({ slice: undefined });
    }}
    onSliced={(id: string) => {
      address.update({ slice: undefined, tab: 'jobs', job: id });
    }}
  />
{/if}
