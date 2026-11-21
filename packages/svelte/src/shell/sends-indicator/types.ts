/** One file on its way to a printer, as the indicator draws it. */
export interface SendsIndicatorRow {
  /** Stable while the row is drawn, and what `cancel` takes. */
  readonly id: string;
  readonly deviceId: string;
  /** The printer's name. */
  readonly printerName: string;
  readonly name: string;
  readonly step:
    'uploading' | 'waiting' | 'asking' | 'checking' | 'replacing' | 'sending';
  /** From 0 to 1 within the current step. Null while the length is unknown. */
  readonly progress: number | null;
  /** True when the signed-in person started it, in this tab or another. */
  readonly mine: boolean;
  /**
   * Who started it, such as `user:ada`. The server names the person only to
   * someone who may read the printer's audit.
   */
  readonly startedBy: string | undefined;
  /** True when the person may stop it. */
  readonly canCancel: boolean;
  /** True while a stop asked for has not answered. */
  readonly stopping: boolean;
}

/** What the indicator reads of the app's Sends store. */
export interface SendsSource {
  readonly rows: readonly SendsIndicatorRow[];
  readonly active: boolean;
  readonly uploading: boolean;
  cancel(id: string): void;
}
