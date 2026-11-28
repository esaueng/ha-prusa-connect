// Write `baseline.json` from what the routes draw right now.
//
// The lane holds every route to its line exactly, so a craft task that removes
// a radius or a primary button makes the lane fail until the line comes down.
// This is how it comes down: run `pnpm run test:craft:record` in the same
// change, read the diff, and commit the new lines with the work that earned
// them. Never run it to make a failure go away.
import { writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { launch } from '../ux/measure.ts';
import { ROUTES } from '../ux/routes.ts';
import { startUxServer } from '../ux/server.ts';
import { ceilingFor } from './ceilings.ts';
import { count, METRICS, openCraftRoute, type CraftCount } from './measure.ts';

const here = dirname(fileURLToPath(import.meta.url));

const say = (line: string): void => {
  process.stdout.write(`${line}\n`);
};

const server = await startUxServer();
const browser = await launch();
const routes: Record<string, CraftCount> = {};

try {
  for (const route of ROUTES) {
    const sheet = await openCraftRoute(browser, server.url, route.path);
    try {
      routes[route.name] = await count(sheet.page);
      // Written straight out rather than through the console, which the lint
      // refuses in this tree. A person watches this run, so it says what it
      // found as it goes.
      say(`${route.name} ${JSON.stringify(routes[route.name])}`);
    } finally {
      await sheet.close();
    }
  }
} finally {
  await browser.close();
  await server.close();
}

let debt = 0;
for (const [name, line] of Object.entries(routes))
  for (const metric of METRICS)
    debt += Math.max(0, line[metric] - ceilingFor(name, metric));

writeFileSync(
  join(here, 'baseline.json'),
  `${JSON.stringify({ debt, routes }, null, 2)}\n`,
  'utf8',
);
say(`debt ${String(debt)}`);
