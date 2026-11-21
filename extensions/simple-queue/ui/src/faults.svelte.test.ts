import { describe, expect, it } from 'vitest';

import { localized } from './faults';
import { t } from './test/fakes';

describe('a refusal in the words of this module', () => {
  it('shows the line of a code this module has, with the English under it', () => {
    expect(
      localized(
        {
          code: 'simple-queue/QUEUE_FULL',
          title: 'the queue of this printer is full',
          detail: undefined,
        },
        t,
      ),
    ).toEqual({
      code: 'simple-queue/QUEUE_FULL',
      title: t('error.simple-queue/QUEUE_FULL'),
      detail: 'the queue of this printer is full',
    });
  });

  it('keeps the English of a code it has no line for', () => {
    const fault = {
      code: 'core.device/GONE',
      title: 'the printer is gone',
      detail: undefined,
    };

    expect(localized(fault, t)).toEqual(fault);
    expect(localized(undefined, t)).toBeUndefined();
  });
});
