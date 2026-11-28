export const MAX_MODEL_BYTES = 20 * 1024 * 1024;
export const MAX_TRIANGLES = 300_000;
export const MAX_COORDINATE_MM = 1_000_000;
export const PLACED_HEADER = new TextEncoder().encode('Prusa Connect Prepare');

export class ModelLimitError extends Error {}
