import { describe, expect, it } from 'vitest';

import { SLOT_NAMES, SLOT_SCREENS } from './types';

describe('SLOT_NAMES', () => {
  // ADR-20260911-4p6 rule 11: every slot name is a string this subpath exports, and
  // the set grows only by a rule added to the record. The dashboard card is
  // ADR-20261008-sww's, the file view ADR-20261027-wvj's, the file tab
  // ADR-20261029-dgv's, the camera section ADR-20261106-xm4's, the camera
  // view ADR-20261106-km2's, and the printer file actions ADR-20261118-5q2's.
  it('publishes the slots of ADR-20260911-4p6 rule 9, ADR-20261118-5q2, ADR-20261008-sww, ADR-20260929-nqd, ADR-20261027-wvj, ADR-20261029-dgv, ADR-20261106-xm4 and ADR-20261106-km2, and no other', () => {
    expect([...SLOT_NAMES]).toEqual([
      'sidebar',
      'page',
      'settings',
      'device-card',
      'device-section',
      'device-tab',
      'dashboard-card',
      'device-action',
      'file-action',
      'print-section',
      'file-view',
      'file-tab',
      'camera-section',
      'camera-view',
      'device-file-action',
      'device-upload-action',
    ]);
  });

  it('names every slot SLOT_SCREENS places a screen', () => {
    for (const slot of Object.keys(SLOT_SCREENS)) {
      expect(SLOT_NAMES).toContain(slot);
    }
  });
});
