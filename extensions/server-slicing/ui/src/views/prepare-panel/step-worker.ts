// The Remus half of the STEP reader, run in a Web Worker that
// `import-step.ts` starts for each read and stops after it. The worker carries
// 4.4 MB of WebAssembly that no other file needs, keeps the page responsive
// while Remus tessellates, and gives all of Remus's memory back when it stops.
//
// Remus reads the exact B-rep geometry of the file, places every body where its
// assembly puts it, converts the file's length unit to millimetres and
// tessellates every body into one binary STL. A mesh over the triangle limit
// is answered by its count alone, so the page never receives it.
//
// The WebAssembly travels inside the worker's script, so nothing reads it with
// `fetch` from beside the bundle. The worker imports no module the entry
// imports and answers with a value, never with an error class of its own.
import type { RemusIo } from 'remus-wasm-io';
import * as glue from 'remus-wasm-io/glue';
import initRemus from 'remus-wasm-io/remus_wasm_io_bg.wasm?init';

/** What the page asks for: a STEP file at one tolerance, under one limit. */
export interface StepRequest {
  bytes: Uint8Array;
  tolerance: number;
  maxTriangles: number;
}

/** What Remus made of a STEP file. A refusal carries Remus's own words. */
export type StepResult =
  | { ok: true; stl: Uint8Array<ArrayBuffer>; bodies: number }
  | { ok: false; reason: 'empty' | 'refused'; detail: string }
  | { ok: false; reason: 'triangles'; triangles: number };

let translator: Promise<RemusIo> | undefined;

const instantiate = async (): Promise<RemusIo> => {
  // wasm-bindgen names the module of every import after the glue file. The
  // cast is needed because a module namespace has no index signature.
  const instance = await initRemus({
    './remus_wasm_io_bg.js': glue as unknown as WebAssembly.ModuleImports,
  });
  glue.__wbg_set_wasm(instance.exports);
  const { __wbindgen_start: start } = instance.exports;
  if (typeof start !== 'function')
    throw new Error('The STEP reader has no start function');
  // The export is a function of no arguments, which the check above narrows
  // only as far as `Function`.
  (start as () => void)();
  return new glue.RemusIo();
};

/** The one translator of this worker. A failed load is forgotten. */
const remus = (): Promise<RemusIo> => {
  translator ??= instantiate().catch((error: unknown) => {
    translator = undefined;
    throw error;
  });
  return translator;
};

const detail = (error: unknown): string =>
  error instanceof Error ? error.message : String(error);

const solidCount = (report: string): number => {
  const parsed: unknown = JSON.parse(report);
  return typeof parsed === 'object' &&
    parsed !== null &&
    'solidCount' in parsed &&
    typeof parsed.solidCount === 'number'
    ? parsed.solidCount
    : 0;
};

const triangleCount = (stl: Uint8Array): number =>
  stl.length < 84
    ? 0
    : new DataView(stl.buffer, stl.byteOffset).getUint32(80, true);

/**
 * Reads every solid of a STEP file and tessellates them into one binary STL.
 * No point of the mesh is farther than `tolerance` millimetres from the exact
 * surface.
 */
const tessellateStep = async ({
  bytes,
  tolerance,
  maxTriangles,
}: StepRequest): Promise<StepResult> => {
  let io: RemusIo;
  try {
    io = await remus();
  } catch (error) {
    return { ok: false, reason: 'refused', detail: detail(error) };
  }
  let solids: Uint8Array;
  let bodies: number;
  try {
    const result = io.importStepStructure(bytes);
    try {
      solids = result.solids;
      bodies = solidCount(result.report);
    } finally {
      result.free();
    }
  } catch (error) {
    return { ok: false, reason: 'refused', detail: detail(error) };
  }
  if (solids.length === 0 || bodies === 0)
    return { ok: false, reason: 'empty', detail: 'The file holds no solid' };
  let stl: Uint8Array;
  try {
    stl = io.exportStl(solids, tolerance);
  } catch (error) {
    return { ok: false, reason: 'refused', detail: detail(error) };
  }
  const triangles = triangleCount(stl);
  if (triangles > maxTriangles)
    return { ok: false, reason: 'triangles', triangles };
  return { ok: true, stl: stl.slice(), bodies };
};

self.addEventListener('message', (event: MessageEvent<StepRequest>) => {
  void tessellateStep(event.data).then((result) => {
    // The STL moves to the page rather than being copied.
    self.postMessage(result, {
      transfer: result.ok ? [result.stl.buffer] : [],
    });
  });
});
