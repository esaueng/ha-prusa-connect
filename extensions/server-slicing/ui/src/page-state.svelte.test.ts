import { describe, expect, it } from 'vitest';

import { compareJobs, filterJobs, jobFacetCounts } from './jobs';
import { PageAddress } from './page-address.svelte';
import { DEFAULT_STATE, searchOf, stateOf } from './page-state';
import {
  compareVersions,
  filterReleases,
  releaseFacetValues,
  releaseRowsOf,
} from './releases';
import {
  adminSlicers,
  everybodysJobs,
  installingSlicers,
  troubledSlicers,
} from './test/fakes';

describe('the order of versions', () => {
  it('puts a prerelease before its final release, alpha before beta before rc', () => {
    const sorted = [
      '2.9.6',
      '3.0.0-alpha12',
      '2.9.6-rc1',
      '2.9.6-alpha2',
      '2.10.0',
      '3.0.0-alpha2',
      '2.9.6-beta1',
    ].sort(compareVersions);

    expect(sorted).toEqual([
      '2.9.6-alpha2',
      '2.9.6-beta1',
      '2.9.6-rc1',
      '2.9.6',
      '2.10.0',
      '3.0.0-alpha2',
      '3.0.0-alpha12',
    ]);
  });
});

describe('the release rows', () => {
  it('joins an offer, the installed copies and an install into one row', () => {
    const rows = releaseRowsOf(installingSlicers);
    const alpha = rows.find((row) => row.version === '3.0.0-alpha12')!;
    const installed = rows.find((row) => row.version === '2.9.6')!;
    const local = rows.find((row) => row.version === '2.9.5')!;

    expect(alpha).toMatchObject({ state: 'installing', channel: 'alpha' });
    expect(installed).toMatchObject({
      state: 'installed',
      isDefault: true,
      digest: 'verified',
      removable: true,
      size: 362_811_392,
    });
    expect(local).toMatchObject({
      state: 'installed',
      digest: 'none',
      removable: false,
      offer: undefined,
    });
  });

  it('counts an old install whose digest is the published one as checked', () => {
    const row = releaseRowsOf({
      ...installingSlicers,
      installed: installingSlicers.installed.map((held) => ({
        ...held,
        verified: false,
      })),
    }).find((entry) => entry.version === '2.9.6');

    expect(row?.digest).toBe('verified');
  });

  it('reads a failed install as its own state', () => {
    const rows = releaseRowsOf(troubledSlicers);

    expect(rows.find((row) => row.version === '3.0.0-alpha12')?.state).toBe(
      'failed',
    );
  });

  it('filters by facet and offers the facet values in a fixed order', () => {
    const rows = releaseRowsOf(adminSlicers);

    expect(releaseFacetValues(rows, 'channel')).toEqual([
      'stable',
      'rc',
      'alpha',
    ]);
    expect(releaseFacetValues(rows, 'line')).toEqual(['3.0', '2.9']);
    expect(
      filterReleases(rows, {
        q: '',
        channel: ['stable'],
        state: ['available'],
        line: [],
      }).map((row) => row.version),
    ).toEqual(['2.9.2']);
  });
});

describe('the job rows', () => {
  it('counts each state with the other filters applied', () => {
    const counts = jobFacetCounts(
      everybodysJobs.jobs,
      { q: 'brackets', state: ['done'], slicer: [], project: [] },
      'state',
    );

    expect(counts.get('done')).toBe(1);
    expect(counts.get('failed')).toBeUndefined();
  });

  it('searches the person, the project and the error', () => {
    const found = filterJobs(everybodysJobs.jobs, {
      q: 'outside of the print',
      state: [],
      slicer: [],
      project: [],
    });

    expect(found.map((job) => job.id)).toEqual(['a4']);
  });

  it('orders by the time a job took', () => {
    const [first] = [...everybodysJobs.jobs].sort((a, b) =>
      compareJobs(a, b, { column: 'duration', ascending: false }),
    );

    expect(first?.id).not.toBe('a2');
  });
});

describe('the page state in the address', () => {
  it('leaves out every default, so the bare address is the page as it opens', () => {
    expect(searchOf(DEFAULT_STATE)).toBe('');
    expect(stateOf('')).toEqual(DEFAULT_STATE);
  });

  it('opens on the build plate, and names the versions in the address', () => {
    expect(stateOf('').tab).toBe('prepare');
    expect(stateOf('?tab=prepare&model=local:a.stl')).toMatchObject({
      tab: 'prepare',
      model: 'local:a.stl',
    });
    expect(searchOf({ ...DEFAULT_STATE, tab: 'slicers' })).toBe('?tab=slicers');
    expect(searchOf({ ...DEFAULT_STATE, model: 'local:a.stl' })).toBe(
      '?model=local%3Aa.stl',
    );
  });

  it('reads back what it wrote', () => {
    const state = {
      ...DEFAULT_STATE,
      tab: 'jobs' as const,
      releases: {
        q: '2.9',
        channel: ['stable', 'rc'],
        state: [],
        line: ['2.9'],
      },
      releaseSort: { column: 'released' as const, ascending: true },
      jobs: { q: 'gear', state: ['failed'], slicer: ['2.9.6'], project: [] },
    };

    expect(stateOf(searchOf(state))).toEqual(state);
  });

  it('opens a job on the Jobs tab and drops a release beside it', () => {
    const state = stateOf('?tab=slicers&job=a4&release=2.9.6');

    expect(state).toMatchObject({ tab: 'jobs', job: 'a4', release: undefined });
  });

  // A file's Slice button sends a person here with the project.
  it('keeps the project the slice dialog is open on', () => {
    const state = stateOf('?slice=local%3Aprojekty%2Fbox.3mf');

    expect(state.slice).toBe('local:projekty/box.3mf');
    expect(stateOf(searchOf(state))).toEqual(state);
  });

  it('reads an unknown tab or sort as its default', () => {
    const state = stateOf('?tab=nope&sort=size.asc&jsort=created.sideways');

    expect(state.tab).toBe('prepare');
    expect(state.releaseSort).toEqual(DEFAULT_STATE.releaseSort);
    expect(state.jobSort).toEqual({ column: 'created', ascending: false });
  });
});

describe('the page address', () => {
  /** A stand-in for window, with a history a test reads back. */
  const browser = (search = '') => {
    const pushed: string[] = [];
    const replaced: string[] = [];
    const location = { pathname: '/m/ns/slicer', search };
    const listeners = new Map<string, () => void>();

    return {
      pushed,
      replaced,
      location,
      history: {
        pushState: (_: unknown, __: string, url: string) => {
          pushed.push(url);
          location.search = url.slice(location.pathname.length);
        },
        replaceState: (_: unknown, __: string, url: string) => {
          replaced.push(url);
          location.search = url.slice(location.pathname.length);
        },
      },
      addEventListener: (type: string, listener: () => void) => {
        listeners.set(type, listener);
      },
      removeEventListener: (type: string) => {
        listeners.delete(type);
      },
      dispatchEvent: () => true,
      fire: (type: string) => listeners.get(type)?.(),
    };
  };

  it('reads the address it mounts on, and pushes a step for a detail', () => {
    const window = browser('?tab=jobs&jstate=failed');
    const address = new PageAddress(window);
    const stop = address.attach();

    expect(address.state.jobs.state).toEqual(['failed']);

    address.go({ job: 'a4' });
    expect(window.pushed).toEqual([
      '/m/ns/slicer?tab=jobs&jstate=failed&job=a4',
    ]);

    address.update({ jobs: { ...address.state.jobs, q: 'gear' } });
    expect(window.replaced).toHaveLength(1);

    // The back button.
    window.location.search = '?tab=jobs';
    window.fire('popstate');
    expect(address.state.job).toBeUndefined();
    expect(address.state.jobs.state).toEqual([]);

    stop();
  });

  it('gives a link to a state of the page', () => {
    const address = new PageAddress(browser());
    address.attach();

    expect(address.href({ release: '2.9.6' })).toBe(
      '/m/ns/slicer?release=2.9.6',
    );
  });
});
