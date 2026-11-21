// The service layer: this module's routes, typed from its own OpenAPI
// fragment, bound onto the client the host handed over. Every request goes
// through it, so the core authorizes each one as the person on the page.
import { sendCommand } from '@prusa/connect-ui/data';
import type { ModuleHost } from '@prusa/connect-ui/slots';
import { bind, type components, type Operations } from 'simple-queue-client';

export type QueueApi = Pick<
  Operations,
  | 'readSummary'
  | 'readBoard'
  | 'readQueue'
  | 'readHistory'
  | 'addEntry'
  | 'removeEntry'
  | 'moveEntry'
  | 'startNext'
  | 'setReady'
  | 'clearHistory'
> & {
  /**
   * Sends one command to the printer through the core's own route, as the
   * person on the page, such as the verb that pauses the print.
   */
  command(device: string, verb: string): Promise<void>;
};
export type Queue = components['schemas']['Queue'];
export type Entry = components['schemas']['Entry'];
export type Printer = components['schemas']['Printer'];
export type Summary = components['schemas']['Summary'];
export type Overview = components['schemas']['Overview'];
export type Board = components['schemas']['Board'];

/** The module's routes, on the host's client. */
export const apiOn = (host: Pick<ModuleHost, 'client'>): QueueApi => ({
  ...bind(host.client),
  // The core's own sender makes the command's key from random bytes. A
  // server on plain HTTP at a LAN address is no secure context, where the
  // browser offers no crypto.randomUUID.
  command: async (device, verb) => {
    await sendCommand(host.client, device, verb);
  },
});

/** The commands the card of a running print sends. */
export const VERB_PAUSE = 'core.command.pause_print';
export const VERB_RESUME = 'core.command.resume_print';
export const VERB_STOP = 'core.command.stop_print';

/** Translates one of this module's texts. */
export type Translate = ModuleHost['t'];
