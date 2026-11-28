// The print settings a person picks over a saved profile, and how the page
// keeps them for each printer. The bounds are the module's own, which
// `internal/settings/print.go` holds on the server too.
import type { components } from 'server-slicing-client';

export type PrintSettings = components['schemas']['PrintSettings'];
export type ProfileSettings = components['schemas']['ProfileSettings'];
export type Supports = NonNullable<PrintSettings['supports']>;

export const LAYER_HEIGHTS_MM = [0.1, 0.15, 0.2, 0.28] as const;
export const SUPPORTS: readonly Supports[] = [
  'off',
  'build_plate',
  'everywhere',
];
/** The brim the switch turns on, when the profile prints none of its own. */
export const BRIM_MM = 5;

const LAYER_MM = { low: 0.05, high: 0.6 };
const WALLS = { low: 1, high: 20 };
const BRIM_LIMIT_MM = 50;

/** What one printer remembers: the profile, the storage and the changed settings. */
export interface PlateChoices {
  readonly profile?: string | undefined;
  readonly storage?: string | undefined;
  readonly settings: PrintSettings;
}

const within = (value: unknown, low: number, high: number): value is number =>
  typeof value === 'number' &&
  Number.isFinite(value) &&
  value >= low &&
  value <= high;

const whole = (value: unknown, low: number, high: number): value is number =>
  within(value, low, high) && Number.isInteger(value);

/** One field of a value read back from the store. `in` narrows a literal key only, hence the cast. */
const fieldOf = (value: unknown, key: string): unknown =>
  typeof value === 'object' && value !== null && key in value
    ? (value as Record<string, unknown>)[key]
    : undefined;

/** The settings with every value the server would refuse left out. */
export const checkedSettings = (value: unknown): PrintSettings => {
  const out: PrintSettings = {};
  const layer = fieldOf(value, 'layer_height_mm');
  if (within(layer, LAYER_MM.low, LAYER_MM.high)) out.layer_height_mm = layer;
  const infill = fieldOf(value, 'infill_percent');
  if (whole(infill, 0, 100)) out.infill_percent = infill;
  const walls = fieldOf(value, 'walls');
  if (whole(walls, WALLS.low, WALLS.high)) out.walls = walls;
  const supports = fieldOf(value, 'supports');
  const known = SUPPORTS.find((one) => one === supports);
  if (known) out.supports = known;
  const brim = fieldOf(value, 'brim_width_mm');
  if (within(brim, 0, BRIM_LIMIT_MM)) out.brim_width_mm = brim;
  return out;
};

/** The choices a stored text names. Anything unreadable is the empty choice. */
export const readChoices = (text: string | null | undefined): PlateChoices => {
  if (!text) return { settings: {} };
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return { settings: {} };
  }
  const name = (key: string): string | undefined => {
    const field = fieldOf(value, key);
    return typeof field === 'string' && field !== '' ? field : undefined;
  };
  return {
    profile: name('profile'),
    storage: name('storage'),
    settings: checkedSettings(fieldOf(value, 'settings')),
  };
};

export const writeChoices = (choices: PlateChoices): string =>
  JSON.stringify({
    profile: choices.profile,
    storage: choices.storage,
    settings: checkedSettings(choices.settings),
  });

/** The store key one printer's choices are kept under. */
export const choicesKey = (printer: string | undefined): string =>
  `plate.choices.${printer ?? 'none'}`;

/** The store key of the printer chosen last. */
export const PRINTER_KEY = 'plate.printer';

/** What the job prints with: the profile's values, then each one the person changed. */
export const settingsShown = (
  profile: ProfileSettings | undefined,
  changed: PrintSettings,
): PrintSettings => ({ ...checkedSettings(profile), ...changed });

/** True when the settings send nothing over the profile. */
export const unchanged = (settings: PrintSettings): boolean =>
  Object.keys(checkedSettings(settings)).length === 0;
