import type { PrintTarget } from '../../print-picker';
import type { SaveToStorageProps } from '../save-to-storage';

export interface DropChoiceProps {
  /** The names of the files dropped on the printer. Undefined keeps it shut. */
  names: readonly string[] | undefined;
  /** The printer's name, for the title. */
  printer: string;
  /**
   * Whether the printer can start a print now, and why not, as the print
   * dialog grades it. Undefined offers no print.
   */
  printTarget: PrintTarget | undefined;
  /**
   * The choices modules add in the device-upload-action slot, such as adding
   * the file to a queue. The file is copied to the printer and the module's
   * action runs while it is on its way.
   */
  actions?: readonly DropAction[] | undefined;
  /**
   * The choice picked when the dialog opens, such as the module's own entry
   * when a module sent the files. Undefined picks Print now on a printer that
   * can print, and a copy otherwise.
   */
  preset?: DropAction | undefined;
  /**
   * Whether the files are kept in a storage of this server, asked in the
   * dialog. Undefined asks nothing here, where the page asks it beside the
   * drop zone.
   */
  keep?: SaveToStorageProps | undefined;
  /**
   * Sends the files. `print` is the place in `names` of the file to start
   * once it lands, which goes to the printer first, and is undefined when
   * every file is only copied. With `action`, each file is copied to the
   * printer and that action runs on it.
   */
  onConfirm: (print: number | undefined, action?: DropAction) => void;
  /**
   * Closes the dialog without sending anything.
   */
  onCancel: () => void;
}

/** A module's choice in the drop question, from the device-upload-action slot. */
export interface DropAction {
  namespace: string;
  id: string;
  title: string;
}
