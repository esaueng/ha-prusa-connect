// The module starts a print by itself only on a printer it may operate. The
// core gives a module no such right when it is installed, so the Queue tab
// has a switch that writes it and takes it away again: an operator edge from
// the printer to this module's own instance, which the host names as
// host.self. A person who may not write grants is refused by the core, and
// the tab shows that refusal.
import type { ModuleHost } from '@prusa/connect-ui/slots';

/** What the Queue tab asks of the server's grants. */
export interface Grants {
  /** Lets this module start prints on the printer by itself. */
  allow(deviceId: string): Promise<void>;
  /** Takes that back, so every print is started by a person again. */
  disallow(deviceId: string): Promise<void>;
  /**
   * Whether the person on the page may switch it: the core takes a grant
   * only from somebody who administers the printer.
   */
  may(deviceId: string): Promise<boolean>;
}

/** Grants written through the host's client, as the person on the page. */
export const grantsOn = (
  host: Pick<ModuleHost, 'client' | 'self'>,
): Grants => ({
  allow: async (deviceId) => {
    await host.client.writeGrant({
      path: { object_type: 'device', object_id: deviceId },
      body: { relation: 'operator', subject: host.self },
    });
  },
  disallow: async (deviceId) => {
    await host.client.withdrawGrant({
      path: { object_type: 'device', object_id: deviceId },
      query: { relation: 'operator', subject: host.self },
    });
  },
  may: async (deviceId) => {
    const device = await host.client.getDevice({
      path: { device_id: deviceId },
    });

    return device.actions?.includes('administer') ?? false;
  },
});
