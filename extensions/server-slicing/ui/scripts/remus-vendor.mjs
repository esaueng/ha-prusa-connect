// The copy of the Remus STEP translator in vendor/remus-wasm-io.
//
// Remus publishes no package. A git dependency on its repository makes pnpm
// prepare the whole repository, which runs its scripts, and pnpm-workspace.yaml
// refuses every dependency script. So the five files of its committed
// wasm-io package are copied here byte for byte from one commit, and this file
// holds them to that commit.
//
//   node scripts/remus-vendor.mjs check       the files match SHA256SUMS
//                                             (offline, part of `check`)
//   node scripts/remus-vendor.mjs verify      the files match the commit in
//                                             SOURCE on GitHub
//   node scripts/remus-vendor.mjs print-sums  print the SHA256SUMS the five
//                                             files call for
//
// The script writes nothing. vendor/remus-wasm-io/README.md says how to copy
// another commit by hand. No path is followed through a link, and a file the
// folder does not list is refused.
//
// REMUS_VENDOR_DIR and REMUS_VENDOR_BASE point it at another folder and
// another server; the tests in remus-vendor.test.mjs use them.
import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';

const dir = path.resolve(
  process.env.REMUS_VENDOR_DIR ??
    path.join(import.meta.dirname, '../vendor/remus-wasm-io'),
);
const BASE =
  process.env.REMUS_VENDOR_BASE ??
  'https://raw.githubusercontent.com/esaueng/remus';
const FILES = [
  'LICENSE-APACHE',
  'NOTICE',
  'remus_wasm_io.d.ts',
  'remus_wasm_io_bg.js',
  'remus_wasm_io_bg.wasm',
];
/** The files written here by hand. */
const OWN = ['README.md', 'glue.d.ts', 'package.json'];
/** Every entry the folder may hold. */
const EXPECTED = new Set([...FILES, ...OWN, 'SHA256SUMS', 'SOURCE']);
const REPOSITORY = 'esaueng/remus';
const PACKAGE = 'crates/wasm-io/pkg';
const SUMS_LINE = /^([0-9a-f]{64}) {2}(\S+)$/u;
const SOURCE_LINE = /^esaueng\/remus ([0-9a-f]{40}) crates\/wasm-io\/pkg\n$/u;

/** @param {Uint8Array} bytes */
const digest = (bytes) => createHash('sha256').update(bytes).digest('hex');

/**
 * One file of the folder, read once, refused unless it is a regular file and
 * not a link.
 * @param {string} name
 */
const read = (name) => {
  const file = path.join(dir, name);
  if (!lstatSync(file).isFile())
    throw new Error(`${name} is not a regular file (a link or a folder)`);
  return readFileSync(file);
};

/** Refuses a folder that is a link, or that holds a file it does not list. */
const listed = () => {
  if (!lstatSync(dir).isDirectory())
    throw new Error(`${dir} is not a folder (a link or a file)`);
  const extra = readdirSync(dir).filter((name) => !EXPECTED.has(name));
  if (extra.length > 0)
    throw new Error(
      `the folder holds files it does not list: ${extra.join(', ')}`,
    );
};

/**
 * The digests SHA256SUMS records: one line per file of FILES, in that order,
 * "<64 hex>  <name>", and a final newline. Anything else is refused.
 * @param {string} text
 * @returns {Map<string, string>}
 */
const parseSums = (text) => {
  const lines = text.split('\n');
  if (lines.pop() !== '' || lines.length !== FILES.length)
    throw new Error(
      `SHA256SUMS must hold exactly ${String(FILES.length)} lines, one per file, and end with a newline`,
    );
  return new Map(
    lines.map((line, i) => {
      const match = SUMS_LINE.exec(line);
      if (!match || match[2] !== FILES[i])
        throw new Error(
          `SHA256SUMS line ${String(i + 1)} must read "<64 hex>  ${String(FILES[i])}"`,
        );
      return [String(match[2]), String(match[1])];
    }),
  );
};

/** @returns {string} the commit SOURCE names, 40 lowercase hex */
const parseSource = () => {
  const match = SOURCE_LINE.exec(read('SOURCE').toString('utf8'));
  if (!match)
    throw new Error(
      `SOURCE must read "${REPOSITORY} <40 lowercase hex> ${PACKAGE}" and a newline`,
    );
  return String(match[1]);
};

/**
 * Reads the folder once and says what is wrong with it. `digests` holds the
 * SHA-256 of the bytes it read, for verify to compare with GitHub.
 */
const examine = () => {
  listed();
  const commit = parseSource();
  const sums = parseSums(read('SHA256SUMS').toString('utf8'));
  for (const name of OWN) read(name);
  /** @type {Map<string, string>} */
  const digests = new Map();
  /** @type {string[]} */
  const problems = [];
  for (const name of FILES) {
    const actual = digest(read(name));
    digests.set(name, actual);
    if (sums.get(name) !== actual)
      problems.push(
        `${name} is ${actual}, SHA256SUMS says ${String(sums.get(name))}`,
      );
  }
  return { commit, digests, problems };
};

/**
 * @param {string} commit
 * @param {string} name
 */
const download = async (commit, name) => {
  const url = `${BASE}/${commit}/${PACKAGE}/${name}`;
  // eslint-disable-next-line no-restricted-globals -- a maintainer's tool run by hand, not module code
  const answer = await fetch(url);
  if (!answer.ok) throw new Error(`${url} answered ${String(answer.status)}`);
  return Buffer.from(await answer.arrayBuffer());
};

const [command] = process.argv.slice(2);
/** @type {string[]} */
let problems = [];
try {
  if (command === 'check') {
    problems = examine().problems;
  } else if (command === 'verify') {
    const found = examine();
    problems = found.problems;
    if (problems.length === 0)
      for (const name of FILES) {
        const upstream = digest(await download(found.commit, name));
        if (found.digests.get(name) !== upstream)
          problems.push(
            `${name} differs from ${REPOSITORY}@${found.commit} (${upstream} there)`,
          );
      }
  } else if (command === 'print-sums') {
    listed();
    process.stdout.write(
      FILES.map((name) => `${digest(read(name))}  ${name}\n`).join(''),
    );
  } else {
    problems.push('usage: remus-vendor.mjs check | verify | print-sums');
  }
} catch (error) {
  problems.push(error instanceof Error ? error.message : String(error));
}

if (problems.length > 0) {
  for (const problem of problems) process.stderr.write(`FAIL  ${problem}\n`);
  process.exit(1);
}
if (command !== 'print-sums')
  process.stdout.write(
    `OK    vendor/remus-wasm-io matches ${command === 'verify' ? 'GitHub' : 'SHA256SUMS'}\n`,
  );
