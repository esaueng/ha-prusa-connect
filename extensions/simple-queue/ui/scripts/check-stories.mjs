// Every component of this module has a story, and each screen that reads the
// server has one for each state it draws on data: ADR-20261023-r3x, held here
// for this module the way the app and the library hold it for theirs.
//
// A component is covered when a `.stories.svelte` file beside it imports it.
// The story file of a screen names Loading, Failed, Refused and Empty.
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';

const root = path.resolve(import.meta.dirname, '../src');
const screens = new Map([
  ['printer-queue.svelte', ['Loading', 'Failed', 'Refused', 'Empty']],
  ['queue-board.svelte', ['Loading', 'Failed', 'Refused', 'Empty']],
]);

const files = readdirSync(root, { recursive: true }).map(String);
const components = files.filter(
  (file) => file.endsWith('.svelte') && !file.endsWith('.stories.svelte'),
);
/** @type {string[]} */
const problems = [];

for (const component of components) {
  const name = path.basename(component);
  const stories = path.join(
    root,
    component.replace(/\.svelte$/u, '.stories.svelte'),
  );
  /** @type {string} */
  let source;
  try {
    source = readFileSync(stories, 'utf8');
  } catch {
    problems.push(`${component} has no story file`);
    continue;
  }
  if (!source.includes(`'./${name}'`)) {
    problems.push(`${path.basename(stories)} does not import ${name}`);
  }
  for (const state of screens.get(name) ?? []) {
    if (!source.includes(`name="${state}"`)) {
      problems.push(`${path.basename(stories)} has no story named ${state}`);
    }
  }
}

if (components.length === 0)
  problems.push('no component was found, so this check proved nothing');

for (const problem of problems) process.stderr.write(`FAIL  ${problem}\n`);
if (problems.length > 0) process.exit(1);
process.stdout.write(
  `OK    ${String(components.length)} components have their stories\n`,
);
