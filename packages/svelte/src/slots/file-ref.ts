// The id the file slots hand a module: one file of a storage of this server,
// written `<storage>:<path>`. A storage name never holds a colon, so the first
// colon splits the two and a path may hold any number of its own.

/** The reference to `path` inside the storage named `storage`. */
export const fileRefOf = (storage: string, path: string): string =>
  `${storage}:${path}`;

/**
 * The storage and the path a reference names, or undefined for a string that
 * is not one: no colon, an empty storage name or an empty path.
 */
export const parseFileRef = (
  ref: string,
): { storage: string; path: string } | undefined => {
  const colon = ref.indexOf(':');
  if (colon <= 0 || colon === ref.length - 1) return undefined;

  return { storage: ref.slice(0, colon), path: ref.slice(colon + 1) };
};

/**
 * The reference to the file at `path` on the printer `deviceId`, such as
 * `<id>:usb/part.bgcode`. A device id never holds a colon, so it splits the
 * way a file reference does.
 */
export const printerFileRefOf = (deviceId: string, path: string): string =>
  `${deviceId}:${path}`;

/**
 * The printer and the path a printer file reference names, or undefined for a
 * string that is not one.
 */
export const parsePrinterFileRef = (
  ref: string,
): { deviceId: string; path: string } | undefined => {
  const file = parseFileRef(ref);

  return file === undefined
    ? undefined
    : { deviceId: file.storage, path: file.path };
};
