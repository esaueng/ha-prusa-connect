// An error, in the words the page shows (frontend-i18n.md rule 5).
//
// `describeFault` looks a title up in the core's catalogue, and this module's
// codes are in this module's own catalogue, so the line is looked up again
// here through the `t` the host bound to this module's namespace. The English
// the server sent stays under it: it names what went wrong this time, and a
// person reading a Czech screen can paste it into a bug report.
import type { FaultText, Translate } from '@prusa/connect-ui/i18n';

/**
 * The fault with this module's line for its code as the title. A key no
 * catalogue holds reads back as the key itself, so a code with no line keeps
 * the server's English.
 */
export function localized(fault: FaultText, t: Translate): FaultText;
export function localized(
  fault: FaultText | undefined,
  t: Translate,
): FaultText | undefined;
export function localized(
  fault: FaultText | undefined,
  t: Translate,
): FaultText | undefined {
  if (fault?.code === undefined) return fault;

  const key = `error.${fault.code}`;
  const line = t(key);
  if (line === key) return fault;

  return { ...fault, title: line, detail: fault.detail ?? fault.title };
}
