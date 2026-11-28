import { expect, it } from 'vitest';

import { type SendSnapshot, stillSendable } from './sendable';

const pressed: SendSnapshot = {
  plate: 'plate',
  printer: 'henry',
  state: 'IDLE',
  action: 'print',
  sendable: true,
};

it('sends nothing while a file is still being read onto the plate', () => {
  expect(stillSendable(pressed, { ...pressed })).toBe(true);
  expect(stillSendable(pressed, { ...pressed, importing: false })).toBe(true);
  expect(stillSendable(pressed, { ...pressed, importing: true })).toBe(false);
});
