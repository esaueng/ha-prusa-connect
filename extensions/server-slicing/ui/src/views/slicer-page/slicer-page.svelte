<!--
  The Slicing page. It opens on the build-plate workspace. The PrusaSlicer
  versions, the jobs and the API reference are pages of their own, reached
  from the workspace's ⋯ menu, each a listing like the app's others with a
  detail for one version or one job. What is on screen is kept in the
  address, so a link, a reload and the back button land where the person was.
  It draws what a SlicerView holds, so a story hands it a view in place of the
  server.
-->
<script lang="ts">
  import { ArrowLeft } from '@lucide/svelte';
  import type { Device } from '@prusa/connect-ui/domain';
  import {
    Button,
    FaultAlert,
    ListingSkeleton,
    type ListingStore,
    PageHeader,
  } from '@prusa/connect-ui/primitives';

  import type { SubmitBody, Translate } from '../../api';
  import { PageAddress } from '../../page-address.svelte';
  import { ReleaseActs } from '../../release-acts.svelte';
  import { releaseRowsOf } from '../../releases';
  import type { SlicerView } from '../../store.svelte';
  import { ApiReference } from '../api-reference';
  import { JobDetail } from '../job-detail';
  import { JobListing } from '../job-listing';
  import type { PrepareApi } from '../prepare-panel/prepare-api';
  import PreparePanel from '../prepare-panel/prepare-panel.svelte';
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
    prepare,
    devices = [],
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
    prepare?: PrepareApi;
    /** The printers the person may see, for the workspace's printer picker. */
    devices?: readonly Device[];
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
  const pageState = $derived(address.state);

  const active = $derived(
    view.jobs?.jobs.filter(
      (job) => job.state === 'queued' || job.state === 'running',
    ).length ?? 0,
  );

  const releaseRow = $derived(
    pageState.release === undefined || view.slicers === undefined
      ? undefined
      : releaseRowsOf(view.slicers).find(
          (row) => row.version === pageState.release,
        ),
  );

  const openJob = $derived(
    pageState.job === undefined
      ? undefined
      : view.jobs?.jobs.find((job) => job.id === pageState.job),
  );

  const workspace = $derived(
    prepare !== undefined &&
      pageState.tab === 'prepare' &&
      pageState.release === undefined &&
      pageState.job === undefined,
  );

  const links = $derived([
    { href: address.href({ tab: 'slicers' }), label: t('slicer.tab.slicers') },
    {
      href: address.href({ tab: 'jobs' }),
      label:
        active > 0
          ? t('slicer.plate.jobsRunning', { count: active })
          : t('slicer.tab.jobs'),
    },
    { href: address.href({ tab: 'api' }), label: t('slicer.tab.api') },
  ]);

  const tabTitle = $derived(
    t(
      pageState.tab === 'prepare'
        ? 'slicer.tab.slicers'
        : `slicer.tab.${pageState.tab}`,
    ),
  );
</script>

<!-- A plain click on a link to another state of this page is taken here. -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div
  class={[
    '@container grid min-w-0 grid-cols-1 content-start',
    // The workspace runs to the edges of the app's content area, so it takes
    // back the padding the app shell's main element gives every page.
    workspace ? '-mx-4 -my-6 @xl/app-shell:-mx-6' : 'gap-4 p-4',
  ]}
  data-part="slicer-page"
  onclick={address.take}
>
  {#if !workspace && pageState.release === undefined && pageState.job === undefined}
    <PageHeader title={prepare ? tabTitle : t('slicer.title')}>
      {#snippet actions()}
        {#if prepare}
          <Button variant="secondary" size="md">
            {#snippet child({ props })}
              <a {...props} href={address.href({ tab: 'prepare' })}
                ><ArrowLeft aria-hidden="true" />{t('slicer.plate.back')}</a
              >
            {/snippet}
          </Button>
        {/if}
      {/snippet}
    </PageHeader>
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
  {:else if view.load === 'failed' && (!workspace || !view.slicers || !view.jobs)}
    <FaultAlert fault={view.fault} />
  {:else if view.slicers && view.jobs}
    {@const slicers = view.slicers}
    {@const jobs = view.jobs}
    {#if view.load === 'failed'}
      <FaultAlert fault={view.fault} />
    {/if}
    {#if workspace && prepare}
      {#key pageState.model}
        <PreparePanel
          beds={slicers.prepare_beds}
          profileSettings={slicers.prepare_settings}
          hasSlicer={slicers.installed.some((one) =>
            one.version.startsWith('2.'),
          )}
          source={pageState.model}
          api={prepare}
          {devices}
          remember={store}
          {links}
          {t}
        />
      {/key}
    {:else if pageState.release !== undefined}
      <FaultAlert fault={view.actionFault} />
      <ReleaseDetail
        version={pageState.release}
        row={releaseRow}
        platform={slicers.platform}
        administrator={jobs.all}
        {acts}
        backHref={address.href({ tab: 'slicers', release: undefined })}
        {t}
      />
    {:else if pageState.job !== undefined}
      <FaultAlert fault={view.actionFault} />
      <JobDetail
        id={pageState.job}
        job={openJob}
        backHref={address.href({ tab: 'jobs', job: undefined })}
        onCancel={(id: string) => void view.cancel(id)}
        {t}
      />
    {:else}
      <FaultAlert fault={view.actionFault} />
      {#if pageState.tab === 'jobs'}
        <JobListing
          {jobs}
          {address}
          {store}
          {t}
          onCancel={(id: string) => void view.cancel(id)}
        />
      {:else if pageState.tab === 'api'}
        <ApiReference {apiBase} {coreBase} {tokensHref} {referenceHref} {t} />
      {:else}
        <ReleaseListing
          {slicers}
          administrator={jobs.all}
          {address}
          {acts}
          {store}
          {t}
          onCheck={() => void view.checkReleases()}
          onInstallUpdate={(version: string) =>
            void view.install(version, false)}
        />
      {/if}
    {/if}
  {/if}
</div>

<ReleaseDialog {acts} {t} />

{#if pageState.slice !== undefined}
  <SliceDialog
    project={pageState.slice}
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
