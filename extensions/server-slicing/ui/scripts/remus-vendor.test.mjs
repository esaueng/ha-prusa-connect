// The tests of remus-vendor.mjs. Each one runs the script on a copy of
// vendor/remus-wasm-io in a temporary folder, and verify downloads from a
// local server instead of GitHub. The script runs with no git variable and
// no git configuration in its environment.
import { execFileSync, spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  cpSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  readlinkSync,
  rmSync,
  symlinkSync,
  unlinkSync,
  writeFileSync,
} from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { after, before, test } from 'node:test';
import assert from 'node:assert/strict';

const SCRIPT = path.join(import.meta.dirname, 'remus-vendor.mjs');
const VENDOR = path.join(import.meta.dirname, '../vendor/remus-wasm-io');
const FILES = [
  'LICENSE-APACHE',
  'NOTICE',
  'remus_wasm_io.d.ts',
  'remus_wasm_io_bg.js',
  'remus_wasm_io_bg.wasm',
];

/**
 * An empty home for every child, so git reads no configuration or
 * attributes from `~/.gitconfig` or `$XDG_CONFIG_HOME/git`.
 */
const HOME = mkdtempSync(path.join(tmpdir(), 'rvt-home-'));

/**
 * `env` without any git variable, with the empty home, and with no global or
 * system git configuration or attributes.
 * @param {NodeJS.ProcessEnv} env
 */
const scrub = (env) => ({
  ...Object.fromEntries(
    Object.entries(env).filter(([key]) => !key.startsWith('GIT_')),
  ),
  HOME,
  XDG_CONFIG_HOME: HOME,
  GIT_CONFIG_GLOBAL: '/dev/null',
  GIT_CONFIG_NOSYSTEM: '1',
  GIT_ATTR_NOSYSTEM: '1',
});

/** The environment of every child. */
const ENV = scrub(process.env);

/** @type {Map<string, Buffer>} what the local server answers, by file name */
const served = new Map();
/** @type {string[]} the paths the server was asked for */
const asked = [];
const server = createServer((request, response) => {
  asked.push(String(request.url));
  const body = served.get(String(request.url).split('/').at(-1) ?? '');
  response.writeHead(body ? 200 : 404).end(body);
});
let base = '';

before(async () => {
  await new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      resolve(undefined);
    });
  });
  const address = server.address();
  if (address === null || typeof address === 'string')
    throw new Error('no port');
  base = `http://127.0.0.1:${String(address.port)}/esaueng/remus`;
});
after(() => {
  server.close();
  rmSync(HOME, { recursive: true, force: true });
});

/** A copy of the vendor folder inside a fresh temporary folder. */
const copy = () => {
  const root = mkdtempSync(path.join(tmpdir(), 'rvt-fixture-'));
  const dir = path.join(root, 'remus-wasm-io');
  cpSync(VENDOR, dir, { recursive: true });
  return { root, dir };
};

/**
 * Runs the script without blocking this process, which serves its downloads.
 * @param {string} dir
 * @param {string[]} args
 * @returns {Promise<{ status: number | null, stdout: string, stderr: string }>}
 */
const run = (dir, ...args) =>
  new Promise((resolve) => {
    const child = spawn(process.execPath, [SCRIPT, ...args], {
      env: { ...ENV, REMUS_VENDOR_DIR: dir, REMUS_VENDOR_BASE: base },
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (chunk) => {
      stdout += String(chunk);
    });
    child.stderr.on('data', (chunk) => {
      stderr += String(chunk);
    });
    child.on('close', (status) => {
      resolve({ status, stdout, stderr });
    });
  });

/**
 * Every entry under `root`, with the SHA-256 of each file and the target of
 * each link, so a test can say nothing changed.
 * @param {string} root
 * @returns {Record<string, string>}
 */
const tree = (root) =>
  Object.fromEntries(
    readdirSync(root, { recursive: true })
      .map(String)
      .map((entry) => {
        const file = path.join(root, entry);
        const stat = lstatSync(file);
        if (stat.isSymbolicLink()) return [entry, `link ${readlinkSync(file)}`];
        if (stat.isDirectory()) return [entry, 'folder'];
        return [
          entry,
          createHash('sha256').update(readFileSync(file)).digest('hex'),
        ];
      }),
  );

/** @param {string} dir */
const serveCopy = (dir) => {
  for (const name of FILES)
    served.set(name, readFileSync(path.join(dir, name)));
};

const reset = () => {
  served.clear();
  asked.length = 0;
};

void test('check passes on the committed copy', async () => {
  const { root, dir } = copy();
  try {
    assert.equal((await run(dir, 'check')).status, 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

void test('check refuses a file that is a link, and a file the folder does not list', async () => {
  const { root, dir } = copy();
  try {
    const outside = path.join(root, 'LICENSE-APACHE');
    cpSync(path.join(dir, 'LICENSE-APACHE'), outside);
    unlinkSync(path.join(dir, 'LICENSE-APACHE'));
    symlinkSync(outside, path.join(dir, 'LICENSE-APACHE'));
    const link = await run(dir, 'check');
    assert.equal(link.status, 1);
    assert.match(link.stderr, /LICENSE-APACHE is not a regular file/u);

    unlinkSync(path.join(dir, 'LICENSE-APACHE'));
    cpSync(outside, path.join(dir, 'LICENSE-APACHE'));
    writeFileSync(path.join(dir, 'local-notes.txt'), 'mine');
    const extra = await run(dir, 'check');
    assert.equal(extra.status, 1);
    assert.match(extra.stderr, /local-notes\.txt/u);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

void test('check and verify refuse a SHA256SUMS that is not exactly one line per file', async () => {
  const { root, dir } = copy();
  try {
    serveCopy(dir);
    const original = readFileSync(path.join(dir, 'SHA256SUMS'), 'utf8');
    const lines = original.trimEnd().split('\n');
    const [first = ''] = lines;
    const variants = {
      duplicate: `${original}${first}\n`,
      'duplicate in place of another': `${[first, first, ...lines.slice(2)].join('\n')}\n`,
      extra: `${original}${'0'.repeat(64)}  extra.bin\n`,
      'one space': `${lines.map((line) => line.replace('  ', ' ')).join('\n')}\n`,
      'no final newline': original.trimEnd(),
      'other order': `${[lines[1], lines[0], ...lines.slice(2)].join('\n')}\n`,
    };
    for (const [what, text] of Object.entries(variants)) {
      writeFileSync(path.join(dir, 'SHA256SUMS'), text);
      for (const command of ['check', 'verify']) {
        const result = await run(dir, command);
        assert.equal(result.status, 1, `${command} accepted ${what}`);
        assert.match(result.stderr, /SHA256SUMS/u, what);
      }
    }
  } finally {
    reset();
    rmSync(root, { recursive: true, force: true });
  }
});

void test('check refuses a SOURCE whose commit is not 40 lowercase hex', async () => {
  const { root, dir } = copy();
  try {
    const [, commit = ''] = readFileSync(
      path.join(dir, 'SOURCE'),
      'utf8',
    ).split(' ');
    const variants = [
      commit.toUpperCase(),
      commit.slice(0, 39),
      `${commit}0`,
      `${commit.slice(0, 39)}g`,
      '../../../../etc',
    ];
    for (const bad of variants) {
      writeFileSync(
        path.join(dir, 'SOURCE'),
        `esaueng/remus ${bad} crates/wasm-io/pkg\n`,
      );
      const result = await run(dir, 'check');
      assert.equal(result.status, 1, bad);
      assert.match(result.stderr, /SOURCE/u, bad);
    }
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

void test('verify compares the bytes it read here with the commit on GitHub', async () => {
  const { root, dir } = copy();
  try {
    serveCopy(dir);
    const same = await run(dir, 'verify');
    assert.equal(same.status, 0, same.stderr);
    const commit = readFileSync(path.join(dir, 'SOURCE'), 'utf8').split(' ')[1];
    assert.ok(
      asked.every((url) =>
        url.startsWith(`/esaueng/remus/${String(commit)}/crates/wasm-io/pkg/`),
      ),
    );

    served.set('NOTICE', Buffer.from('another NOTICE'));
    const other = await run(dir, 'verify');
    assert.equal(other.status, 1);
    assert.match(other.stderr, /NOTICE differs/u);
  } finally {
    reset();
    rmSync(root, { recursive: true, force: true });
  }
});

void test('print-sums prints the SHA256SUMS of the files and writes nothing', async () => {
  const { root, dir } = copy();
  try {
    const before = tree(root);
    const committed = await run(dir, 'print-sums');
    assert.equal(committed.status, 0, committed.stderr);
    assert.equal(
      committed.stdout,
      readFileSync(path.join(dir, 'SHA256SUMS'), 'utf8'),
    );
    assert.deepEqual(tree(root), before);

    writeFileSync(path.join(dir, 'NOTICE'), 'new NOTICE');
    const changed = await run(dir, 'print-sums');
    assert.match(
      changed.stdout,
      new RegExp(
        `^${createHash('sha256').update('new NOTICE').digest('hex')} {2}NOTICE$`,
        'mu',
      ),
    );
    // SHA256SUMS written from that output passes check.
    writeFileSync(path.join(dir, 'SHA256SUMS'), changed.stdout);
    assert.equal((await run(dir, 'check')).status, 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

void test('the script has no command that writes', async () => {
  const { root, dir } = copy();
  try {
    const before = tree(root);
    const result = await run(dir, 'update', 'a'.repeat(40));
    assert.equal(result.status, 1);
    assert.match(result.stderr, /usage/u);
    assert.deepEqual(tree(root), before);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

const RULE = 'extensions/server-slicing/ui/vendor/remus-wasm-io/** -text';
/**
 * Holds a repository's staged copy of the vendor folder to what the vendor
 * README promises. The staged `.gitattributes` files and the staged vendor
 * folder are checked out into a fresh repository that converts every text
 * file to CRLF (`core.autocrlf=true`, `core.eol=crlf`), with no configuration
 * of this machine; every checked-out file must still hash to its line in the
 * checked-out SHA256SUMS. So the staged attributes, whatever their form, must
 * keep git from converting the folder, a rule only in `.git/info/attributes`
 * counts for nothing, and a staged but uncommitted update passes.
 * @param {string} root
 * @param {string} vendor
 * @param {Record<string, string | undefined>} env
 */
const assertStagedCopy = (root, vendor, env) => {
  /**
   * @param {string} cwd
   * @param {string[]} args
   * @param {string} [input]
   */
  const git = (cwd, args, input) =>
    execFileSync('git', ['-c', 'core.hooksPath=/dev/null', ...args], {
      cwd,
      env,
      input,
      maxBuffer: 64 << 20,
    });
  const rel = path.relative(root, vendor).split(path.sep).join('/');
  const staged = git(root, [
    'ls-files',
    '--stage',
    '--',
    ':(glob)**/.gitattributes',
    rel,
  ]).toString('utf8');
  const objects = path.resolve(
    root,
    git(root, ['rev-parse', '--git-path', 'objects']).toString('utf8').trim(),
  );

  const checkout = mkdtempSync(path.join(tmpdir(), 'rvt-checkout-'));
  try {
    git(checkout, ['init', '--quiet']);
    // The new repository reads the blobs from the one under test, and
    // writes nothing into it.
    writeFileSync(
      path.join(checkout, '.git', 'objects', 'info', 'alternates'),
      `${objects}\n`,
    );
    git(checkout, ['update-index', '--index-info'], staged);
    git(checkout, [
      '-c',
      'core.autocrlf=true',
      '-c',
      'core.eol=crlf',
      'checkout-index',
      '--all',
      '--force',
    ]);
    const sums = readFileSync(path.join(checkout, rel, 'SHA256SUMS'), 'utf8');
    for (const name of FILES) {
      const bytes = readFileSync(path.join(checkout, rel, name));
      const hash = createHash('sha256').update(bytes).digest('hex');
      assert.ok(
        sums.includes(`${hash}  ${name}\n`),
        `${name} checks out as ${hash}, which SHA256SUMS does not list`,
      );
    }
  } finally {
    rmSync(checkout, { recursive: true, force: true });
  }
};

void test('git converts no line ending, so the staged files match SHA256SUMS', () => {
  const root = execFileSync('git', ['rev-parse', '--show-toplevel'], {
    cwd: VENDOR,
    env: ENV,
    encoding: 'utf8',
  }).trim();
  assertStagedCopy(root, VENDOR, ENV);
});

void test('a staged update that is not committed yet passes the same test', async () => {
  const root = mkdtempSync(path.join(tmpdir(), 'rvt-fixture-'));
  try {
    const env = {
      ...ENV,
      GIT_AUTHOR_NAME: 'remus-vendor test',
      GIT_AUTHOR_EMAIL: 'test@example.invalid',
      GIT_COMMITTER_NAME: 'remus-vendor test',
      GIT_COMMITTER_EMAIL: 'test@example.invalid',
    };
    /** @param {string[]} args */
    const git = (...args) =>
      execFileSync('git', ['-c', 'core.hooksPath=/dev/null', ...args], {
        cwd: root,
        env,
      });
    const real = execFileSync('git', ['rev-parse', '--show-toplevel'], {
      cwd: VENDOR,
      env: ENV,
      encoding: 'utf8',
    }).trim();
    const dir = path.join(root, path.relative(real, VENDOR));
    cpSync(VENDOR, dir, { recursive: true });
    writeFileSync(
      path.join(root, '.gitattributes'),
      execFileSync('git', ['show', ':.gitattributes'], { cwd: real, env: ENV }),
    );
    git('init', '--quiet');
    git('add', '.');
    git('commit', '--quiet', '-m', 'fixture');

    // The README's update, staged and not committed.
    writeFileSync(path.join(dir, 'NOTICE'), 'a newer NOTICE\n');
    const printed = await run(dir, 'print-sums');
    assert.equal(printed.status, 0, printed.stderr);
    writeFileSync(path.join(dir, 'SHA256SUMS'), printed.stdout);
    git('add', '--', dir);

    assertStagedCopy(root, dir, env);
    assert.equal((await run(dir, 'check')).status, 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

void test('a rule only in .git/info/attributes does not stand in for the staged one', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'rvt-fixture-'));
  try {
    const real = execFileSync('git', ['rev-parse', '--show-toplevel'], {
      cwd: VENDOR,
      env: ENV,
      encoding: 'utf8',
    }).trim();
    const dir = path.join(root, path.relative(real, VENDOR));
    cpSync(VENDOR, dir, { recursive: true });
    // Text files convert on checkout here, unless a staged rule stops it.
    writeFileSync(path.join(root, '.gitattributes'), '* text=auto\n');
    /** @param {string[]} args */
    const git = (...args) =>
      execFileSync('git', ['-c', 'core.hooksPath=/dev/null', ...args], {
        cwd: root,
        env: ENV,
      });
    git('init', '--quiet');
    writeFileSync(path.join(root, '.git', 'info', 'attributes'), `${RULE}\n`);
    git('add', '.');
    assert.throws(() => {
      assertStagedCopy(root, dir, ENV);
    }, /checks out as/u);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

/**
 * A throwaway repository holding the vendor folder where this repository
 * holds it, with `attributes` as its root .gitattributes, staged.
 * @param {string} attributes
 * @param {NodeJS.ProcessEnv} env
 */
const stagedFixture = (attributes, env) => {
  const root = mkdtempSync(path.join(tmpdir(), 'rvt-fixture-'));
  const real = execFileSync('git', ['rev-parse', '--show-toplevel'], {
    cwd: VENDOR,
    env: ENV,
    encoding: 'utf8',
  }).trim();
  const dir = path.join(root, path.relative(real, VENDOR));
  cpSync(VENDOR, dir, { recursive: true });
  writeFileSync(path.join(root, '.gitattributes'), attributes);
  /** @param {string[]} args */
  const git = (...args) =>
    execFileSync('git', ['-c', 'core.hooksPath=/dev/null', ...args], {
      cwd: root,
      env,
    });
  try {
    git('init', '--quiet');
    git('add', '.');
  } catch (error) {
    rmSync(root, { recursive: true, force: true });
    throw error;
  }
  return { root, dir };
};

void test('a git attributes file in the home of the user reaches no test git', () => {
  const hostile = mkdtempSync(path.join(tmpdir(), 'rvt-xdg-'));
  try {
    mkdirSync(path.join(hostile, 'git'));
    writeFileSync(
      path.join(hostile, 'git', 'attributes'),
      'NOTICE working-tree-encoding=UTF-16\n',
    );
    const env = scrub({
      ...process.env,
      HOME: hostile,
      XDG_CONFIG_HOME: hostile,
    });
    const { root, dir } = stagedFixture(`${RULE}\n`, env);
    try {
      assertStagedCopy(root, dir, env);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  } finally {
    rmSync(hostile, { recursive: true, force: true });
  }
});

void test('a later rule that changes the bytes of a vendored file is refused, an unrelated one is not', () => {
  for (const [later, refused] of /** @type {const} */ ([
    ['*.d.ts text eol=crlf', true],
    ['"*.js" text eol=crlf', true],
    ['extensions/server-slicing/ui/vendor/** text', true],
    ['docs/** text eol=crlf', false],
    ['*.wasm binary', false],
  ])) {
    const { root, dir } = stagedFixture(`${RULE}\n${later}\n`, ENV);
    try {
      if (refused)
        assert.throws(
          () => {
            assertStagedCopy(root, dir, ENV);
          },
          /checks out as/u,
          later,
        );
      else assertStagedCopy(root, dir, ENV);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  }
});

void test('every git of these tests reads no system or user configuration or attributes', () => {
  // git reads the system attributes file unless GIT_ATTR_NOSYSTEM is set, and
  // writing that file to prove it would change this machine for everyone.
  const env = scrub({
    ...process.env,
    GIT_ATTR_NOSYSTEM: '0',
    GIT_CONFIG_NOSYSTEM: '0',
    GIT_DIR: '/elsewhere',
    HOME: '/home/someone',
  });
  assert.equal(env.GIT_ATTR_NOSYSTEM, '1');
  assert.equal(env.GIT_CONFIG_NOSYSTEM, '1');
  assert.equal(env.GIT_CONFIG_GLOBAL, '/dev/null');
  assert.ok(!('GIT_DIR' in env));
  assert.equal(env.HOME, HOME);
  assert.equal(env.XDG_CONFIG_HOME, HOME);
  assert.deepEqual(readdirSync(HOME), []);
  assert.equal(ENV.GIT_ATTR_NOSYSTEM, '1');
});
