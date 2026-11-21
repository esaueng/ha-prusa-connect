// What happens when a control is pressed, as something the consumer supplies.
//
// The split this exists for: the button owns whether it is offered, whether it
// is enabled and what it says when it is not, all of which is this tier's
// answer and identical in every consumer. What pressing it does is the
// consumer's, and another consumer may do more than this one: it can collect
// options first.
//
// So the effect is injected rather than branched. A branch inside the button
// on which consumer is running would put both arms in both builds and grow one
// arm per difference; replacing the button through a slot would fork the
// capability check, the disabled reason, the label and the aria with it.
//
// The contract is here and the defaults belong to the application, because a
// component at any tier above reads the contract and only an application
// chooses the defaults.
import type { LifecycleVerb } from './lifecycle';

export interface CommandAction {
  /**
   * Resolving means "the product is done with the press", not "the printer
   * started". A cancelled dialog resolves having done nothing, which is why
   * this is not allowed to report success: the fleet's own state stays the
   * only truth about what the machine is doing.
   */
  run: (deviceId: string) => Promise<void>;
  /**
   * True when the press opens something before acting. Read by the button to
   * mark itself (an ellipsis, `aria-haspopup="dialog"`), so the hint cannot
   * drift from the behaviour the way a separate `variant` prop would.
   */
  opensDialog?: boolean;
}

export type CommandActions = Record<LifecycleVerb, CommandAction>;

/** Which file to print, and where it comes from. */
export interface PrintRequest {
  readonly deviceId: string;
  /**
   * The file on the printer, storage-prefixed and relative, such as
   * `usb/bracket.bgcode`. With a storage it is where the file lands.
   */
  readonly path: string;
  /** A storage on this server holding the file. Without one the printer already has the file. */
  readonly storage?: string;
  /** The file's path inside that storage. The core refuses a storage without it. */
  readonly source?: string;
  /**
   * With a storage, delete the file the printer holds at `path` first. The
   * person has agreed to it after the core answered `FILE_NAME_TAKEN`.
   */
  readonly replace?: boolean | undefined;
  /**
   * With a storage, send under a free name beside the file the printer holds
   * at `path`, such as `usb/bracket[1].bgcode`. The answer's path names it.
   */
  readonly keepBoth?: boolean | undefined;
}

/** What came back: the printer took the command, or it refused and said why. */
export interface PrintOutcome {
  /** The file that was started, as the printer addresses it. */
  readonly path: string;
  /** Whether the printer took the command. */
  readonly ok: boolean;
  /** The transfer this server ran to get the file there, absent without a storage. */
  readonly transferId: string | undefined;
  /** This API's code for the refusal, which is what a screen branches on. */
  readonly faultCode: string | undefined;
  /** Why the printer refused, in the printer's own words, for a person to read. */
  readonly machineReason: string | undefined;
  /** The invocation's id. The print's record is read back under it. */
  readonly commandId: string | undefined;
}

/**
 * Getting one file printing. It is injected for the reason the lifecycle
 * actions are: a consumer may route the file somewhere else before the
 * printer sees it, and the sheet around it stays the same.
 */
export type PrintAction = (request: PrintRequest) => Promise<PrintOutcome>;
