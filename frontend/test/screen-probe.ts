import type { SlotRegistry } from '@prusa/connect-ui/slots';

// What the counting screen records, read by the app's boundary tests.
export const screen: {
  mounts: number;
  failing: boolean;
  /** The registry the app hands its screens, from the last mount. */
  registry?: SlotRegistry | undefined;
} = { mounts: 0, failing: false };
