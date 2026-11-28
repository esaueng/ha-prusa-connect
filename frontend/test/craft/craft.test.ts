// The craft lane (earlier research task 2). Every route is opened in a real
// Chromium and counted: radii, text sizes, greys, borders, primary buttons,
// icons, punctuation and figures.
//
// Two lines hold each count. `baseline.json` is what the screens carry today,
// and a route has to match it exactly: a count that went up is a drift and
// fails, and a count that went down fails too, until the craft task that
// improved it lowers the line in the same change. `ceilings.ts` is where the
// counts are going, read off the theme's own tokens, and the failure message
// says how far a route still is from it.
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import type { Browser } from 'playwright';
import { afterAll, beforeAll, describe, expect, inject, it } from 'vitest';

import { launch } from '../ux/measure.ts';
import { ROUTES } from '../ux/routes.ts';
import { ceilingFor, CEILINGS } from './ceilings.ts';
import {
  count,
  METRICS,
  openCraftRoute,
  type CraftCount,
  type Metric,
} from './measure.ts';

const here = dirname(fileURLToPath(import.meta.url));

interface Baseline {
  /** How far every line together still sits above its ceiling. */
  readonly debt: number;
  readonly routes: Record<string, CraftCount>;
}

const baseline = JSON.parse(
  readFileSync(join(here, 'baseline.json'), 'utf8'),
) as Baseline;

const url = inject('uxBaseUrl');

let browser: Browser;

beforeAll(async () => {
  browser = await launch();
});

afterAll(async () => {
  await browser.close();
});

/** What a count that moved should say, so a failure names the fix. */
const said = (
  route: string,
  metric: Metric,
  measured: number,
  line: number,
): string => {
  const ceiling = ceilingFor(route, metric);
  const direction =
    measured > line
      ? `${route} draws ${String(measured)} where its line is ${String(line)}: a new one crept in`
      : `${route} is down to ${String(measured)} from ${String(line)}: lower the line in baseline.json`;

  return [
    `${metric}: ${direction}.`,
    `The ceiling is ${String(ceiling)}, because ${CEILINGS[metric].because}.`,
  ].join(' ');
};

describe('every route holds its craft counts', () => {
  for (const route of ROUTES) {
    it(`${route.name} matches its line`, async () => {
      const line = baseline.routes[route.name];
      expect(line, `${route.name} has no line in baseline.json`).toBeDefined();

      const sheet = await openCraftRoute(browser, url, route.path);
      try {
        const measured = await count(sheet.page);

        for (const metric of METRICS) {
          expect(
            measured[metric],
            said(route.name, metric, measured[metric], line![metric]),
          ).toBe(line![metric]);
        }
      } finally {
        await sheet.close();
      }
    });
  }
});

// The ceilings are the tokens' own numbers. A theme that grew a fifth radius
// role would make this lane pass a page it should have failed, so the two
// numbers are compared rather than written down twice.
describe('the ceilings come from the theme', () => {
  const tokens = readFileSync(
    join(
      here,
      '..',
      '..',
      '..',
      'packages',
      'svelte',
      'src',
      'styles',
      'tokens.css',
    ),
    'utf8',
  );

  const rolesIn = (family: string): string[] => {
    const block = tokens.slice(tokens.indexOf('@theme inline'));
    const names = [
      ...block.matchAll(new RegExp(`^\\s*--${family}-([a-z0-9-]+):`, 'gm')),
    ].map((match) => match[1]!);

    return [...new Set(names)];
  };

  // The role names are the ported design system's, which merge 41a1efb7 put
  // in place of the four named roles the theme carried before.
  it('allows one radius per role the theme declares', () => {
    const roles = ['xs', 'sm', 'md'];
    for (const role of roles) expect(rolesIn('radius')).toContain(role);
    expect(CEILINGS.radii.allowed).toBe(roles.length);
  });

  it('allows one text size per role the theme declares', () => {
    const roles = ['xs', 'sm', 'base', 'lg', '2xl'];
    for (const role of roles) expect(rolesIn('text')).toContain(role);
    expect(CEILINGS.sizes.allowed).toBe(roles.length);
  });

  // The lane paints a probe with `--primary-fill` and counts the controls the
  // browser gave that same colour. A name the theme does not declare resolves
  // to nothing, the probe comes back fully transparent, and every unfilled
  // control on the page is then counted as the thing to press.
  it('declares the fill the lane asks a primary button for', () => {
    expect(tokens).toMatch(/^\s*--primary-fill:\s*\S+;\s*$/m);
  });

  it('gives every count a ceiling and a reason', () => {
    for (const metric of METRICS) {
      expect(CEILINGS[metric].allowed).toBeGreaterThanOrEqual(0);
      expect(CEILINGS[metric].because.length).toBeGreaterThan(20);
    }
  });
});

// A line above its ceiling is the work the craft tasks still owe. This states
// it as a number so the debt cannot grow while every route passes.
describe('the debt the craft tasks still owe', () => {
  it('matches what the lines add up to', () => {
    let owed = 0;
    for (const route of ROUTES) {
      const line = baseline.routes[route.name];
      if (line === undefined) continue;
      for (const metric of METRICS)
        owed += Math.max(0, line[metric] - ceilingFor(route.name, metric));
    }

    expect(
      owed,
      'the debt moved: set `debt` in baseline.json to what the lines now add up to',
    ).toBe(baseline.debt);
  });
});
