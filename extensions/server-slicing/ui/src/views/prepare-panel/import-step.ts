import { STLLoader } from 'three/addons/loaders/STLLoader.js';

import { MAX_TRIANGLES, ModelLimitError } from './import-limits';
import type { MeshReader } from './mesh-import';
import type { StepRequest, StepResult } from './step-worker';

/** The largest distance between a STEP mesh and its exact surface, in mm. */
export const STEP_TOLERANCE_MM = 0.01;

/** How long one STEP read may take before its worker is stopped. */
export const STEP_TIMEOUT_MS = 60_000;

/** Remus could not read a STEP file. The message is its own English reason. */
export class StepError extends Error {
  readonly reason: 'empty' | 'refused';

  constructor(reason: 'empty' | 'refused', detail: string) {
    super(detail);
    this.name = 'StepError';
    this.reason = reason;
  }
}

/**
 * The STEP file read, but its mesh at this tolerance is too large or took too
 * long. A larger tolerance can fix both, so the editor keeps the file open.
 */
export class StepMeshLimitError extends ModelLimitError {
  readonly reason: 'triangles' | 'time';

  constructor(reason: 'triangles' | 'time') {
    super(
      reason === 'time'
        ? 'The STEP reader ran out of time'
        : 'The STEP mesh has too many triangles',
    );
    this.name = 'StepMeshLimitError';
    this.reason = reason;
  }
}

// ISO 10303-21 opens every exchange file with this keyword.
const SIGNATURE = 'ISO-10303-21;';

/** The read was stopped through its signal; its caller no longer wants it. */
export class StepAbortedError extends Error {
  constructor() {
    super('The STEP read was stopped');
    this.name = 'StepAbortedError';
  }
}

/**
 * Runs one read in a worker of its own. The worker stops when the answer
 * arrives, when the time runs out, or when `signal` aborts, so a read nobody
 * waits for gives its CPU and memory back at once.
 */
const tessellate = (
  request: StepRequest,
  signal: AbortSignal | undefined,
): Promise<StepResult> => {
  if (signal?.aborted) return Promise.reject(new StepAbortedError());
  const worker = new Worker(new URL('./step-worker.ts', import.meta.url), {
    type: 'module',
  });
  return new Promise<StepResult>((resolve, reject) => {
    const stop = (error?: Error): void => {
      clearTimeout(timer);
      signal?.removeEventListener('abort', aborted);
      worker.terminate();
      if (error) reject(error);
    };
    const aborted = (): void => {
      stop(new StepAbortedError());
    };
    const timer = setTimeout(() => {
      stop(new StepMeshLimitError('time'));
    }, STEP_TIMEOUT_MS);
    signal?.addEventListener('abort', aborted);
    worker.addEventListener('message', (event: MessageEvent<StepResult>) => {
      stop();
      resolve(event.data);
    });
    worker.addEventListener('error', (event) => {
      event.preventDefault();
      stop(
        new StepError('refused', event.message || 'The STEP reader stopped'),
      );
    });
    try {
      worker.postMessage(request);
    } catch (error) {
      // A request the browser cannot copy to the worker, such as bytes whose
      // buffer was moved away, stops the worker at once.
      stop(
        new StepError(
          'refused',
          error instanceof Error ? error.message : String(error),
        ),
      );
    }
  });
};

/**
 * Reads a STEP file through Remus in a worker of its own. Every body of the
 * file comes back in one mesh, placed where its assembly puts it.
 */
export const stepReader: MeshReader = {
  matches: (bytes) =>
    new TextDecoder()
      .decode(bytes.subarray(0, 64))
      .trimStart()
      .startsWith(SIGNATURE),
  async read(bytes, { tolerance = STEP_TOLERANCE_MM, signal } = {}) {
    const result = await tessellate(
      { bytes, tolerance, maxTriangles: MAX_TRIANGLES },
      signal,
    );
    if (result.ok)
      return {
        parts: [{ geometry: new STLLoader().parse(result.stl.buffer) }],
        preservePlacement: false,
        bodies: result.bodies,
      };
    if (result.reason === 'triangles')
      throw new StepMeshLimitError('triangles');
    throw new StepError(result.reason, result.detail);
  },
};
