import { describe, expect, it, vi } from 'vitest';

import { apiOn, VERB_PAUSE } from './api';

describe('the queue service', () => {
  // The page runs on plain HTTP at a LAN address, which is no secure context,
  // so the command's key is made without crypto.randomUUID.
  it('sends a printer command with a fresh key, as the person on the page', async () => {
    const sendDeviceCommand = vi.fn(() =>
      Promise.resolve({ ok: true, command_id: 'c1' }),
    );
    const api = apiOn({
      client: { sendDeviceCommand } as unknown as Parameters<
        typeof apiOn
      >[0]['client'],
    });
    const uuid = vi.spyOn(crypto, 'randomUUID');

    await api.command('mk4', VERB_PAUSE);

    const [request] = sendDeviceCommand.mock.calls[0] as unknown as [
      { path: { device_id: string }; body: { verb: string; key: string } },
    ];
    expect(request.path.device_id).toBe('mk4');
    expect(request.body.verb).toBe(VERB_PAUSE);
    expect(request.body.key).toMatch(/^[0-9a-f]{32}$/u);
    expect(uuid).not.toHaveBeenCalled();
  });
});
