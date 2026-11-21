// The gate behind the voice guide in earlier research.
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { WIZARD_CATALOGUES } from '../shell/setup-wizard/catalogues';
import { CORE_CATALOGUES } from './catalogues';

/** The application's own few lines, under the `app` namespace. */
const APP_CATALOGUE = fileURLToPath(
  new URL('../../../../frontend/src/locales/en.json', import.meta.url),
);

const english = async (): Promise<Record<string, string>> => {
  const app = JSON.parse(await readFile(APP_CATALOGUE, 'utf8')) as Record<
    string,
    string
  >;
  const lines: Record<string, string> = {
    ...CORE_CATALOGUES.en,
    ...WIZARD_CATALOGUES.en,
  };

  for (const [key, message] of Object.entries(app)) {
    if (!key.startsWith('_')) lines[key] = message;
  }

  // `error.<code>` is the fault registry's own English, and
  // `printerCode.xbuddy.<code>` and `printerButton.xbuddy.<name>` are what the printer shows
  // on its own screen.
  const OTHERS_WORDS = ['error.', 'printerCode.', 'printerButton.'];

  return Object.fromEntries(
    Object.entries(lines).filter(
      ([key]) => !OTHERS_WORDS.some((prefix) => key.startsWith(prefix)),
    ),
  );
};

/** A value that is a wire word rather than a sentence. */
const WIRE_VALUES = new Set([
  'prusa_printer_settings.ini',
  'config.d/fractal.yaml',
]);

/** A key whose text names something rather than saying something about it. */
const LABEL_PARTS = new Set([
  'badge',
  'caption',
  'column',
  'count',
  'for',
  'heading',
  'label',
  'legend',
  'name',
  'placeholder',
  'prompt',
  'shown',
  'step',
  'tab',
  'title',
]);

const LABELS = new Set([
  // The accessible name of the per-row control that changes somebody's role.
  'people.role.changeFor',
  // The caption under the history card.
  'history.note',
  // The name of the drop choice's option that prints one file of several.
  'printerDetailFiles.drop.printOne',
]);

/** A line whose parts are joined by a middle dot is a legend. */
const JOINED = ' · ';

const isLabel = (key: string, value: string): boolean =>
  LABELS.has(key) ||
  LABEL_PARTS.has(key.slice(key.lastIndexOf('.') + 1)) ||
  value.includes(JOINED);

/** A finite verb or an imperative. */
const VERB =
  /\b(is|are|was|were|has|have|had|does|do|did|can|cannot|could|may|might|will|would|must|needs?|asks?|answers?|reads?|writes?|shows?|says?|takes?|gives?|goes|comes?|joins?|links?|holds?|keeps?|adds?|sets?|sends?|starts?|stops?|opens?|closes?|runs?|works?|reaches?|appears?|arrives?|happens?|stays?|signs?|prints?|drops?|draws?|calls?|accepts?|refuses?|changes?|waits?|carries|counts?|decides?|installs?|uninstalls?|restarts?|switches|removes?|creates?|reports?|lists?|sees|knows|names?|lets?|leaves?|matches|offers?|approves?|chooses?|clears?|compares?|copies|copy|enters?|finds?|fits?|moves?|picks?|presses|press|puts?|tells?|tries|try|turns?|types?|uses?|saves?)\b/i;

/** The message with its parameters taken out: `{type}` is not an English word. */
const prose = (value: string): string => value.replaceAll(/\{[^}]*\}/g, ' x ');

const wordsIn = (value: string): number =>
  prose(value).trim().split(/\s+/).length;

/** Whether a value has to end with a stop. */
const mustClose = (key: string, value: string): boolean =>
  !isLabel(key, value) &&
  !value.endsWith('}') &&
  wordsIn(value) > 4 &&
  VERB.test(prose(value));

/** Whether a value is allowed to end with a stop. */
const mayClose = (key: string, value: string): boolean =>
  !isLabel(key, value) && wordsIn(value) >= 3 && VERB.test(prose(value));

/** One word per thing, from the table in earlier research. */
const DROPPED: readonly { readonly of: RegExp; readonly use: string }[] = [
  { of: /\bmachines?\b/i, use: 'printer, or device for any other kind' },
  { of: /^Devices you hold$/, use: 'Printers you may see' },
  { of: /\bmint(ed|ing|s)?\b/i, use: 'create, for a credential' },
  { of: /\blets?\s+\w+(\s+\w+)?\s+in\b/i, use: 'approve' },
  { of: /^Let in$/i, use: 'Approved' },
  { of: /\bsilent\b/i, use: 'Not reporting' },
  { of: /\bnot reported\b/i, use: 'No reading yet, or Not reporting' },
  { of: /\bgone\b/i, use: 'Offline' },
  { of: /\bunreachable\b/i, use: 'Offline' },
  { of: /\basking\b/i, use: 'Reading' },
  { of: /^Finish$/, use: 'Done' },
  { of: /\bfinish the\b/i, use: 'Done, or the verb the flow ends on' },
  { of: /^Keep$/, use: 'Cancel' },
  { of: /^Users$/, use: 'People' },
  { of: /^(API tokens|Credentials)$/, use: 'API credentials' },
  { of: /^App \{/, use: 'Version {version}' },
  { of: /\b(LIVE|SNAPSHOT)\b/, use: 'Live, Snapshot' },
  { of: /\blive view\b/i, use: 'Live' },
  { of: /\bnewest frame\b/i, use: 'the view from this device' },
  { of: /^The answers$/, use: 'Answer' },
  { of: /^Outcome$/, use: 'Result, for accepted or refused' },
];

/** A unit typed into a message, where `formatUnit` should have put it on. */
const GLUED_UNIT = /(\{[^}]*\}\s*(°C|°|%|mm)\b)|((°C|°|%|mm)\s*\{)/;

const failures = async (
  test: (key: string, value: string) => string | undefined,
): Promise<string[]> => {
  const lines = await english();

  return Object.entries(lines)
    .map(([key, value]) => {
      const said = test(key, value);

      return said === undefined ? undefined : `${key}: ${said}`;
    })
    .filter((said): said is string => said !== undefined);
};

describe('every English line follows the voice guide', () => {
  it('opens with a capital, unless it is a wire value', async () => {
    expect(
      await failures((_key, value) =>
        /^[a-z]/.test(value) && !WIRE_VALUES.has(value)
          ? `starts lowercase, "${value}"`
          : undefined,
      ),
    ).toEqual([]);
  });

  it('gives a label no period', async () => {
    expect(
      await failures((key, value) =>
        value.endsWith('.') && !value.endsWith('..') && !mayClose(key, value)
          ? `is a label and carries a period, "${value}"`
          : undefined,
      ),
    ).toEqual([]);
  });

  it('gives a sentence one', async () => {
    expect(
      await failures((key, value) =>
        mustClose(key, value) && !/[.?!:]$/.test(value)
          ? `is a sentence and carries no period, "${value}"`
          : undefined,
      ),
    ).toEqual([]);
  });

  it('never types a unit next to a parameter', async () => {
    expect(
      await failures((_key, value) =>
        GLUED_UNIT.test(value)
          ? `writes a unit into the message, "${value}". Use formatUnit`
          : undefined,
      ),
    ).toEqual([]);
  });

  it('uses one word per thing', async () => {
    expect(
      await failures((_key, value) => {
        const found = DROPPED.find((word) => word.of.test(value));

        return found === undefined
          ? undefined
          : `"${value}" carries a word the table drops. Write ${found.use}`;
      }),
    ).toEqual([]);
  });
});
