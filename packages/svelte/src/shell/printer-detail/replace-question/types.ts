export interface ReplaceQuestionProps {
  /** The name the printer already holds. Undefined closes the question. */
  name: string | undefined;
  /** True when the file is to be printed once it replaces the other. */
  print?: boolean | undefined;
  /**
   * True when the printer lists its file read-only and refuses to delete it,
   * so the question says why and offers no replace.
   */
  readOnly?: boolean | undefined;
  onReplace: () => void;
  /** Sends the file under a free name and leaves the printer's file alone. */
  onKeepBoth: () => void;
  /** Prints the file the printer already holds and sends nothing, for a print. */
  onPrintHeld?: (() => void) | undefined;
  /**
   * Hands the file the printer already holds to the module's choice the
   * person picked for the dropped file, such as a queue, and sends nothing.
   * Given only for a file dropped with such a choice.
   */
  onUseHeld?: (() => void) | undefined;
  onKeep: () => void;
}
