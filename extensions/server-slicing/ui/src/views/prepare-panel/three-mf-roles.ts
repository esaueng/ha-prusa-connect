import { ModelLimitError } from './import-limits';
import { children, idOf } from './three-mf-xml';

const PRUSA = 'Metadata/Slic3r_PE_model.config';
const BAMBU = 'Metadata/model_settings.config';
const PRUSA_3 = 'Metadata/PrusaSlicer3_project.json';

const prusaTypes = new Map([
  ['ModelPart', true],
  ['NegativeVolume', false],
  ['ParameterModifier', false],
  ['SupportBlocker', false],
  ['SupportEnforcer', false],
]);
const bambuTypes = new Map([
  ['normal_part', true],
  ['negative_part', false],
  ['modifier_part', false],
  ['support_blocker', false],
  ['support_enforcer', false],
]);

interface TriangleRange {
  first: number;
  last: number;
  printable: boolean;
}

export type ProjectRoles =
  | { kind: 'prusa'; objects: Map<number, TriangleRange[]> }
  | { kind: 'bambu'; objects: Map<number, Map<number, boolean>> };

export class ThreeMFRoleError extends Error {
  constructor() {
    super('Could not determine the 3MF part roles');
  }
}

const prusaRole = (volume: Element): boolean => {
  let kind: string | undefined;
  let modifier: string | undefined;
  for (const meta of children(volume, 'metadata')) {
    const key = meta.getAttribute('key');
    const value = meta.getAttribute('value')?.trim() ?? '';
    if (key === 'volume_type') {
      if (kind !== undefined) throw new ThreeMFRoleError();
      kind = value;
    }
    if (key === 'modifier') {
      if (modifier !== undefined) throw new ThreeMFRoleError();
      modifier = value;
    }
  }

  // Older PrusaSlicer versions mark modifier volumes with a boolean key.
  if (modifier !== undefined && modifier !== '0' && modifier !== '1')
    throw new ThreeMFRoleError();
  const printable = prusaTypes.get(kind ?? 'ModelPart');
  if (printable === undefined) throw new ThreeMFRoleError();
  return printable && modifier !== '1';
};

const prusaRanges = (object: Element): TriangleRange[] =>
  Array.from(children(object, 'volume'), (volume) => ({
    first: idOf(volume, 'firstid'),
    last: idOf(volume, 'lastid'),
    printable: prusaRole(volume),
  })).sort((one, other) => one.first - other.first);

const bambuParts = (object: Element): Map<number, boolean> => {
  const parts = new Map<number, boolean>();
  for (const part of children(object, 'part')) {
    const id = idOf(part, 'id');
    const printable = bambuTypes.get(part.getAttribute('subtype') ?? '');
    if (printable === undefined || parts.has(id)) throw new ThreeMFRoleError();
    parts.set(id, printable);
  }
  return parts;
};

const objectsOf = <T>(
  root: Element,
  rolesOf: (object: Element) => T,
): Map<number, T> => {
  const objects = new Map<number, T>();
  for (const object of children(root, 'object')) {
    const id = idOf(object, 'id');
    if (objects.has(id)) throw new ThreeMFRoleError();
    objects.set(id, rolesOf(object));
  }
  return objects;
};

export const readProjectRoles = async (
  readXML: (name: string) => Promise<Element | undefined>,
  has: (name: string) => boolean,
): Promise<ProjectRoles | undefined> => {
  const prusa = has(PRUSA);
  const bambu = has(BAMBU);
  if (has(PRUSA_3) || (prusa && bambu)) throw new ThreeMFRoleError();
  if (!prusa && !bambu) return undefined;

  try {
    const root = await readXML(prusa ? PRUSA : BAMBU);
    if (root?.localName !== 'config' || root.namespaceURI !== null)
      throw new ThreeMFRoleError();
    return prusa
      ? { kind: 'prusa', objects: objectsOf(root, prusaRanges) }
      : { kind: 'bambu', objects: objectsOf(root, bambuParts) };
  } catch (error) {
    if (error instanceof ModelLimitError) throw error;
    throw new ThreeMFRoleError();
  }
};

export const selectPrusaTriangles = (
  ranges: TriangleRange[] | undefined,
  triangles: number,
): { mask: Uint8Array; count: number } => {
  if (!ranges) throw new ThreeMFRoleError();
  const mask = new Uint8Array(triangles);
  let next = 0;
  let count = 0;
  for (const { first, last, printable } of ranges) {
    if (first !== next || last < first || last >= triangles)
      throw new ThreeMFRoleError();
    next = last + 1;
    if (printable) {
      mask.fill(1, first, next);
      count += next - first;
    }
  }
  if (next !== triangles) throw new ThreeMFRoleError();
  return { mask, count };
};
