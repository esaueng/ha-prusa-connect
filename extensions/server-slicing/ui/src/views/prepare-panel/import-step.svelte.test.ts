import { page } from 'vitest/browser';
import { describe, expect, it, vi } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { STLLoader } from 'three/addons/loaders/STLLoader.js';
import type { BufferGeometry } from 'three';

import { t } from '../../test/fakes';
import { MAX_MODEL_BYTES, ModelLimitError } from './import-limits';
import {
  STEP_TIMEOUT_MS,
  STEP_TOLERANCE_MM,
  StepError,
  StepMeshLimitError,
} from './import-step';
import { type ImportedMesh, readMeshFile } from './mesh-import';
import type { PrepareApi } from './prepare-api';
import {
  bed,
  charlie,
  henry,
  loadFixture,
  memory,
  profileSettings,
  testPrepare,
} from './prepare-fixtures';
import PreparePanel from './prepare-panel.svelte';

// block-and-peg-inch.step: an AP214 file in inches with two bodies, a block
// 1 × 0.5 × 0.25 in and a peg 0.25 in across and 0.5 in tall beside it.
// nist-ftc-11-ap203.stp: test case FTC 11 of the NIST MBE PMI project, an
// AP203 part exported by a CAD system. NIST publishes it without restriction.
// ball.step: a whole sphere 200 mm across, which makes about 100,000
// triangles at the default tolerance and ten times that at 0.001 mm.
// large-ball.step: a sphere 1 m across, about 490,000 triangles at the default
// tolerance and 50,000 at 0.1 mm.

const size = (geometry: BufferGeometry): number[] => {
  geometry.computeBoundingBox();
  const box = geometry.boundingBox;
  if (!box) throw new Error('No bounding box');
  return box.max
    .clone()
    .sub(box.min)
    .toArray()
    .map((n) => Math.round(n * 1000) / 1000);
};

const triangles = (geometry: BufferGeometry): number =>
  geometry.getAttribute('position').count / 3;

const only = (imported: ImportedMesh): BufferGeometry => {
  expect(imported.parts).toHaveLength(1);
  const [part] = imported.parts;
  if (!part) throw new Error('No part');
  return part.geometry;
};

const stepFile = (text: string, name = 'part.step'): File =>
  new File([text], name);

const refusal = async (file: File): Promise<unknown> =>
  readMeshFile(file).then(
    () => undefined,
    (error: unknown) => error,
  );

const EMPTY_STEP = `ISO-10303-21;
HEADER;
FILE_DESCRIPTION((''),'2;1');
FILE_NAME('','',(''),(''),'','','');
FILE_SCHEMA(('AUTOMOTIVE_DESIGN'));
ENDSEC;
DATA;
ENDSEC;
END-ISO-10303-21;
`;

describe('the STEP reader', () => {
  it('reads a STEP file in inches as millimetres, every body in one part', async () => {
    const imported = await readMeshFile(
      await loadFixture('block-and-peg-inch.step'),
    );
    expect(imported.bodies).toBe(2);
    expect(imported.preservePlacement).toBe(false);
    const geometry = only(imported);
    // 1.625 × 0.5 × 0.5 in: the block, and the peg 0.5 in beside it.
    expect(size(geometry)).toEqual([41.275, 12.7, 12.7]);
    geometry.dispose();
  });

  it('reads an AP203 part from a CAD system at the tolerance it is given', async () => {
    const file = await loadFixture('nist-ftc-11-ap203.stp', 'FTC-11.STP');
    const fine = only(await readMeshFile(file));
    const coarse = only(await readMeshFile(file, { tolerance: 0.1 }));

    // A disc 63 mm across and 3 mm thick. A chord of its edge lies inside the
    // exact circle by no more than the tolerance.
    const [width = 0, depth = 0, height = 0] = size(fine);
    for (const across of [width, depth]) {
      expect(across).toBeLessThanOrEqual(63);
      expect(across).toBeGreaterThanOrEqual(63 - 2 * STEP_TOLERANCE_MM);
    }
    expect(height).toBe(3);
    expect(triangles(coarse)).toBeLessThan(triangles(fine) / 4);
    fine.dispose();
    coarse.dispose();
  });

  it('refuses a mesh over the triangle limit in its worker, and the page keeps running', async () => {
    const file = await loadFixture('ball.step');
    // The worker counts the triangles and sends no mesh, while the page keeps
    // running: a timer on the page fires all through the read.
    let ticks = 0;
    const timer = setInterval(() => (ticks += 1), 10);
    const started = performance.now();
    const refused = await readMeshFile(file, { tolerance: 0.001 }).then(
      () => undefined,
      (error: unknown) => error,
    );
    const elapsed = performance.now() - started;
    clearInterval(timer);
    expect(refused).toBeInstanceOf(StepMeshLimitError);
    expect(refused).toBeInstanceOf(ModelLimitError);
    expect(ticks).toBeGreaterThanOrEqual(Math.floor(elapsed / 10 / 3));
  });

  it('refuses a STEP file with no solid, one Remus cannot read, and one that is not STEP', async () => {
    const empty = await refusal(stepFile(EMPTY_STEP));
    expect(empty).toBeInstanceOf(StepError);
    expect(empty instanceof StepError && empty.reason).toBe('empty');

    const broken = await refusal(stepFile('ISO-10303-21;\nHEADER;\nnot STEP'));
    expect(broken).toBeInstanceOf(StepError);
    expect(broken instanceof StepError && broken.reason).toBe('refused');
    expect(broken instanceof StepError && broken.message).not.toBe('');

    const renamed = await refusal(stepFile('solid cube\nendsolid cube\n'));
    expect(renamed).toBeInstanceOf(Error);
    expect(renamed).not.toBeInstanceOf(StepError);
  });

  it('stops the worker and its timer at once when the request cannot be posted', async () => {
    const terminate = vi.spyOn(Worker.prototype, 'terminate');
    const postMessage = vi
      .spyOn(Worker.prototype, 'postMessage')
      .mockImplementation(() => {
        throw new DOMException('The buffer was moved away', 'DataCloneError');
      });
    const file = await loadFixture('block-and-peg-inch.step');
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      const failed = await refusal(file);
      expect(failed).toBeInstanceOf(StepError);
      expect(failed instanceof StepError && failed.message).toBe(
        'The buffer was moved away',
      );
      expect(terminate).toHaveBeenCalledTimes(1);
      // The one-minute timer was cleared: passing the minute stops nothing.
      vi.advanceTimersByTime(STEP_TIMEOUT_MS + 1_000);
      expect(terminate).toHaveBeenCalledTimes(1);
      expect(vi.getTimerCount()).toBe(0);
    } finally {
      vi.useRealTimers();
      terminate.mockRestore();
      postMessage.mockRestore();
    }
  });

  it('stops the worker when its signal aborts', async () => {
    const terminate = vi.spyOn(Worker.prototype, 'terminate');
    const postMessage = vi.spyOn(Worker.prototype, 'postMessage');
    try {
      const controller = new AbortController();
      const reading = readMeshFile(await loadFixture('large-ball.step'), {
        signal: controller.signal,
      }).then(
        () => undefined,
        (error: unknown) => error,
      );
      await expect.poll(() => postMessage.mock.calls.length).toBe(1);
      expect(terminate).not.toHaveBeenCalled();
      controller.abort();
      expect(terminate).toHaveBeenCalledTimes(1);
      expect(await reading).toBeInstanceOf(Error);
    } finally {
      terminate.mockRestore();
      postMessage.mockRestore();
    }
  });
});

/** The workspace with an API that reads `read` from Storage. */
const workspace = (more: Partial<PrepareApi> = {}, source?: string) => ({
  api: { ...testPrepare, ...more },
  beds: { 'core-one-petg': bed },
  profileSettings,
  hasSlicer: true,
  devices: [henry, charlie],
  remember: memory(),
  source,
  t,
});

const status = () => page.getByTestId('plate-status');

const pick = async (container: HTMLElement, file: File): Promise<void> => {
  const picker =
    container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!picker) throw new Error('The workspace drew no file picker');
  await page.elementLocator(picker).upload(file);
};

describe('a STEP file on the build plate', () => {
  it('puts a STEP assembly on the plate as one part and saves it in millimetres', async () => {
    const save = vi.fn(testPrepare.save);
    await page.viewport(1440, 900);
    const { container } = await render(PreparePanel, {
      props: workspace({ save }),
    });
    await pick(container, await loadFixture('block-and-peg-inch.step'));

    await expect.element(status()).toMatchTextContent('1 part');
    await expect
      .element(
        page.getByText(
          'The 2 bodies of the STEP file are one part on the plate.',
        ),
      )
      .toBeVisible();
    await expect
      .element(page.getByLabelText('STEP mesh (mm)', { exact: true }))
      .toHaveValue(STEP_TOLERANCE_MM);

    await page
      .getByRole('button', { name: 'More ways to finish the plate' })
      .click();
    await page.getByRole('menuitem', { name: 'Save positioned STL' }).click();
    await expect.poll(() => save.mock.calls.length).toBe(1);
    const [, name, body] = save.mock.calls[0] ?? [];
    expect(name).toMatch(/^block-and-peg-inch-placed-[0-9a-f]{32}\.stl$/u);
    if (!body) throw new Error('No plate was saved');
    const written = new STLLoader().parse(await body.arrayBuffer());
    expect(size(written)).toEqual([41.275, 12.7, 12.7]);
    written.computeBoundingBox();
    expect(written.boundingBox?.min.z).toBe(0);
    written.dispose();
  });

  it('names a STEP file Remus refuses, with its reason, and one with no solid', async () => {
    const { container } = await render(PreparePanel, {
      props: workspace(),
    });
    await pick(container, stepFile('ISO-10303-21;\nHEADER;\nnot', 'x.stp'));
    await expect
      .element(page.getByText(/^x\.stp: Could not read this STEP file: .+/u))
      .toBeVisible();
    await pick(container, await loadFixture('no-solid.step'));
    await expect
      .element(
        page.getByText(
          'no-solid.step: The STEP file holds no solid body. Export the part as a solid.',
        ),
      )
      .toBeVisible();
    await expect.element(status()).toMatchTextContent('0 parts');
  });

  it('asks for a larger tolerance when a STEP mesh is over the limit, and uses it for the next file', async () => {
    const ball = await loadFixture('large-ball.step');
    const { container } = await render(PreparePanel, {
      props: workspace(),
    });
    await pick(container, ball);
    await expect
      .element(
        page.getByText(
          'large-ball.step: At this STEP mesh tolerance it has more than 300,000 triangles. Enter a larger tolerance and add it again.',
        ),
      )
      .toBeVisible();
    await expect.element(status()).toMatchTextContent('0 parts');

    const tolerance = page.getByLabelText('STEP mesh (mm)', { exact: true });
    await tolerance.fill('0.1');
    tolerance.element().dispatchEvent(new Event('change', { bubbles: true }));
    await pick(container, await loadFixture('large-ball.step'));
    await expect.element(status()).toMatchTextContent('1 part');
  });

  it('names the size limit for a STEP file over it, and for one from Storage', async () => {
    const limit = /The model exceeds the import limits/u;
    const huge = new File(
      [new Uint8Array(MAX_MODEL_BYTES + 1)],
      'huge-assembly.step',
    );
    const screen = await render(PreparePanel, { props: workspace() });
    await pick(screen.container, huge);
    await expect.element(page.getByText(limit)).toBeVisible();
    await screen.unmount();

    // The Storage read stops at the limit, before the file is whole.
    await render(PreparePanel, {
      props: workspace(
        {
          read: () =>
            Promise.reject(new ModelLimitError('Model exceeds 20 MiB')),
        },
        'local:huge.step',
      ),
    });
    await expect.element(page.getByText(limit)).toBeVisible();
  });

  it('adds a STEP file from Storage that ends after a dropped one, and stops no worker early', async () => {
    let finish: (file: File) => void = () => undefined;
    const slow = new Promise<File>((resolve) => {
      finish = resolve;
    });
    const terminate = vi.spyOn(Worker.prototype, 'terminate');
    const postMessage = vi.spyOn(Worker.prototype, 'postMessage');
    try {
      await page.viewport(1440, 900);
      const { container } = await render(PreparePanel, {
        props: workspace({ read: () => slow }, 'local:slow.step'),
      });
      await pick(container, await loadFixture('block-and-peg-inch.step'));
      // The dropped file waits for the Storage read, which ends now.
      finish(await loadFixture('nist-ftc-11-ap203.stp'));

      await expect.element(status()).toMatchTextContent('2 parts');
      expect(page.getByRole('alert').elements()).toHaveLength(0);
      // One worker for each file, each stopped once, after its answer.
      expect(postMessage).toHaveBeenCalledTimes(2);
      expect(terminate).toHaveBeenCalledTimes(2);
    } finally {
      terminate.mockRestore();
      postMessage.mockRestore();
    }
  });

  it('stops the STEP worker when the workspace closes during a read', async () => {
    const terminate = vi.spyOn(Worker.prototype, 'terminate');
    let posted: () => void = () => undefined;
    const started = new Promise<void>((resolve) => {
      posted = resolve;
    });
    // Called below with apply on the worker it belongs to.
    // eslint-disable-next-line @typescript-eslint/unbound-method
    const post = Worker.prototype.postMessage;
    const postMessage = vi
      .spyOn(Worker.prototype, 'postMessage')
      .mockImplementation(function (
        this: Worker,
        ...args: Parameters<Worker['postMessage']>
      ) {
        post.apply(this, args);
        posted();
      });
    try {
      const ball = await loadFixture('large-ball.step');
      const screen = await render(PreparePanel, {
        props: workspace(
          { read: () => Promise.resolve(ball) },
          'local:large-ball.step',
        ),
      });
      await started;
      // No answer can arrive before this line: the worker's reply is a task,
      // and only microtasks have run since the request was posted.
      expect(terminate).not.toHaveBeenCalled();
      await screen.unmount();
      expect(terminate).toHaveBeenCalledTimes(1);
    } finally {
      terminate.mockRestore();
      postMessage.mockRestore();
    }
  });
});
