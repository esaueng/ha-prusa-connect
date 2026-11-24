<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { everybodysJobs, longJobs, t } from '../../test/fakes';
  import JobDetail from './job-detail.svelte';

  const { Story } = defineMeta({
    title: 'Server slicing/JobDetail',
    component: JobDetail,
    args: { t, backHref: '?tab=jobs', onCancel: () => undefined },
  });

  const jobOf = (id: string) =>
    [...everybodysJobs.jobs, ...longJobs.jobs].find((job) => job.id === id);
  const finished = jobOf('a3');
</script>

<Story name="Done" args={{ id: 'a3', job: jobOf('a3') }} />
<Story
  name="STL with saved profile"
  args={{
    id: 'a3',
    job: finished && {
      ...finished,
      source: { storage: 'local', path: 'models/box.stl' },
      profile: 'core-one-petg',
    },
  }}
/>
<Story name="Slicing" args={{ id: 'a1', job: jobOf('a1') }} />
<Story name="Waiting" args={{ id: 'a2', job: jobOf('a2') }} />
<Story name="Failed" args={{ id: 'a4', job: jobOf('a4') }} />
<Story name="Failed with a log" args={{ id: 'b1', job: jobOf('b1') }} />
<Story name="Not listed" args={{ id: 'gone', job: undefined }} />
