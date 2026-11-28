import { describe, expect, it } from 'vitest';

import {
  checkedSettings,
  readChoices,
  settingsShown,
  unchanged,
  writeChoices,
} from './print-settings';

describe('the print settings kept for a printer', () => {
  it('reads back what it wrote', () => {
    const choices = {
      profile: 'core-one-petg',
      storage: 'local',
      settings: {
        layer_height_mm: 0.28,
        infill_percent: 25,
        walls: 3,
        supports: 'build_plate' as const,
        brim_width_mm: 5,
      },
    };
    expect(readChoices(writeChoices(choices))).toEqual(choices);
  });

  it('answers the empty choice for nothing kept or text it cannot read', () => {
    for (const text of [null, undefined, '', '{', '"text"', '[]', 'null'])
      expect(readChoices(text)).toEqual({
        profile: undefined,
        storage: undefined,
        settings: {},
      });
  });

  it('leaves out every value the server would refuse', () => {
    expect(
      checkedSettings({
        layer_height_mm: 0.01,
        infill_percent: 101,
        walls: 0,
        supports: 'trees',
        brim_width_mm: -1,
      }),
    ).toEqual({});
    expect(
      checkedSettings({
        infill_percent: 12.5,
        walls: 2.5,
        layer_height_mm: 0.6,
      }),
    ).toEqual({ layer_height_mm: 0.6 });
    expect(checkedSettings('layer_height_mm')).toEqual({});
  });

  it('shows the profile’s values under the ones the person changed', () => {
    expect(
      settingsShown(
        { filament_type: 'PETG', layer_height_mm: 0.2, infill_percent: 15 },
        { infill_percent: 40 },
      ),
    ).toEqual({ layer_height_mm: 0.2, infill_percent: 40 });
    expect(unchanged({})).toBe(true);
    expect(unchanged({ walls: 3 })).toBe(false);
  });
});
