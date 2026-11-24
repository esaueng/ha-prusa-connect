// What the Slicing page shows, as the address bar spells it, so a reload, a
// link and the back button land on the same tab, the same filters and the
// same open release or job.
//
// The page has one route of its own, `/m/<namespace>/slicer`, so the state
// rides in the query: `?tab=jobs&jstate=failed&job=a4`. A value equal to its
// default is left out, so the bare address is the page as it first opens.
import {
  DEFAULT_JOB_SORT,
  type JobFilter,
  type JobSort,
  NO_JOB_FILTER,
} from './jobs';
import {
  DEFAULT_RELEASE_SORT,
  NO_RELEASE_FILTER,
  type ReleaseFilter,
  type ReleaseSort,
} from './releases';

export const TABS = ['slicers', 'prepare', 'jobs', 'api'] as const;
export type Tab = (typeof TABS)[number];

export interface PageState {
  tab: Tab;
  releases: ReleaseFilter;
  releaseSort: ReleaseSort;
  jobs: JobFilter;
  jobSort: JobSort;
  /** The release open in the detail, by version. */
  release: string | undefined;
  /** The job open in the detail, by id. */
  job: string | undefined;
  /**
   * The project the slice dialog is open on, as `<storage>:<path>`. A file's
   * page sends a person here with it.
   */
  slice: string | undefined;
  /** The STL opened from Storage into Prepare. */
  model: string | undefined;
}

export const DEFAULT_STATE: PageState = {
  tab: 'slicers',
  releases: NO_RELEASE_FILTER,
  releaseSort: DEFAULT_RELEASE_SORT,
  jobs: NO_JOB_FILTER,
  jobSort: DEFAULT_JOB_SORT,
  release: undefined,
  job: undefined,
  slice: undefined,
  model: undefined,
};

/** The longest search the page keeps. */
const MAX_QUERY = 120;

const listOf = (text: string | null): string[] =>
  text === null || text === ''
    ? []
    : text
        .split(',')
        .map((value) => value.trim())
        .filter((value) => value !== '');

const sortOf = <Column extends string>(
  text: string | null,
  columns: readonly Column[],
  fallback: { column: Column; ascending: boolean },
): { column: Column; ascending: boolean } => {
  const [column, order] = (text ?? '').split('.');
  if (!columns.includes(column as Column)) return fallback;

  return { column: column as Column, ascending: order === 'asc' };
};

const sortText = (sort: { column: string; ascending: boolean }): string =>
  `${sort.column}.${sort.ascending ? 'asc' : 'desc'}`;

const given = (text: string | null): string | undefined =>
  text === null || text === '' ? undefined : text;

/** The tab a query names. An open job belongs to the Jobs tab, whatever it says. */
const tabOf = (tab: string, job: string | undefined): Tab => {
  if (job !== undefined) return 'jobs';

  return TABS.find((known) => known === tab) ?? DEFAULT_STATE.tab;
};

/** The state a query names. An unknown or malformed value is its default. */
export const stateOf = (search: string): PageState => {
  const query = new URLSearchParams(search);
  const tab = query.get('tab') ?? '';
  const job = given(query.get('job'));
  const release = given(query.get('release'));

  return {
    tab: tabOf(tab, job),
    releases: {
      q: (query.get('q') ?? '').slice(0, MAX_QUERY),
      channel: listOf(query.get('channel')),
      state: listOf(query.get('state')),
      line: listOf(query.get('line')),
    },
    releaseSort: sortOf(
      query.get('sort'),
      ['version', 'released'],
      DEFAULT_RELEASE_SORT,
    ),
    jobs: {
      q: (query.get('jq') ?? '').slice(0, MAX_QUERY),
      state: listOf(query.get('jstate')),
      slicer: listOf(query.get('jslicer')),
      project: listOf(query.get('jproject')),
    },
    jobSort: sortOf(
      query.get('jsort'),
      ['created', 'duration'],
      DEFAULT_JOB_SORT,
    ),
    release: job === undefined ? release : undefined,
    job,
    slice: given(query.get('slice')),
    model: given(query.get('model')),
  };
};

/** The query for a state, with every default left out, starting with `?` when not empty. */
export const searchOf = (state: PageState): string => {
  const query = new URLSearchParams();
  const put = (key: string, value: string, fallback = ''): void => {
    if (value !== fallback) query.set(key, value);
  };

  put('tab', state.tab, DEFAULT_STATE.tab);
  put('q', state.releases.q.trim());
  put('channel', state.releases.channel.join(','));
  put('state', state.releases.state.join(','));
  put('line', state.releases.line.join(','));
  put('sort', sortText(state.releaseSort), sortText(DEFAULT_RELEASE_SORT));
  put('jq', state.jobs.q.trim());
  put('jstate', state.jobs.state.join(','));
  put('jslicer', state.jobs.slicer.join(','));
  put('jproject', state.jobs.project.join(','));
  put('jsort', sortText(state.jobSort), sortText(DEFAULT_JOB_SORT));
  put('release', state.release ?? '');
  put('job', state.job ?? '');
  put('slice', state.slice ?? '');
  put('model', state.model ?? '');

  const text = query.toString();

  return text === '' ? '' : `?${text}`;
};
