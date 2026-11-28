import { BufferAttribute, BufferGeometry, Matrix4 } from 'three';

import {
  MAX_COORDINATE_MM,
  MAX_TRIANGLES,
  ModelLimitError,
} from './import-limits';
import type { MeshPart, MeshReader } from './mesh-import';
import { LOCAL_FILE, ThreeMFArchive } from './three-mf-archive';

const CORE = 'http://schemas.microsoft.com/3dmanufacturing/core/2015/02';
const PRODUCTION =
  'http://schemas.microsoft.com/3dmanufacturing/production/2015/06';
const MATERIAL =
  'http://schemas.microsoft.com/3dmanufacturing/material/2015/02';
const RELS = 'http://schemas.openxmlformats.org/package/2006/relationships';
const MODEL_REL =
  'http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel';
const DEFAULT_MODEL = '3D/3dmodel.model';
const MAX_XML_TAGS = 1_400_000;
const MAX_VISITS = 50_000;
const MAX_DEPTH = 64;

const units = new Map([
  ['micron', 0.001],
  ['millimeter', 1],
  ['centimeter', 10],
  ['inch', 25.4],
  ['foot', 304.8],
  ['meter', 1000],
]);

const invalid = (message: string): Error =>
  new Error(`Invalid 3MF: ${message}`);

const children = function* (
  parent: Element,
  name: string,
): IterableIterator<Element> {
  const { namespaceURI } = parent;
  for (const element of parent.children)
    if (element.localName === name && element.namespaceURI === namespaceURI)
      yield element;
};

const child = (parent: Element, name: string): Element | undefined => {
  for (const element of children(parent, name)) return element;
  return undefined;
};

const idOf = (element: Element, attribute: string): number => {
  const text = element.getAttribute(attribute)?.trim() ?? '';
  const id = Number(text);
  if (!/^\d+$/u.test(text) || !Number.isSafeInteger(id))
    throw invalid(`invalid ${attribute}`);
  return id;
};

const decimal = (text: string): number => {
  const value = Number(text);
  if (
    !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/iu.test(text) ||
    !Number.isFinite(value)
  )
    throw invalid('invalid coordinate or transform');
  return value;
};

const transformOf = (element: Element, unit: number): Matrix4 => {
  const value = element.getAttribute('transform');
  if (value === null) return new Matrix4();
  const fields = value.trim().split(/\s+/u);
  if (fields.length !== 12) throw invalid('wrong transform length');
  const [
    a = 0,
    b = 0,
    c = 0,
    d = 0,
    e = 0,
    f = 0,
    g = 0,
    h = 0,
    i = 0,
    x = 0,
    y = 0,
    z = 0,
  ] = fields.map(decimal);

  // 3MF uses row vectors. Three.js uses column vectors.
  const matrix = new Matrix4().set(
    a,
    d,
    g,
    x * unit,
    b,
    e,
    h,
    y * unit,
    c,
    f,
    i,
    z * unit,
    0,
    0,
    0,
    1,
  );
  const determinant = matrix.determinant();
  if (!Number.isFinite(determinant) || determinant === 0)
    throw invalid('singular or non-finite transform');
  return matrix;
};

const packagePath = (base: string, target: string): string => {
  const path = decodeURIComponent(target);
  if (!path || path.startsWith('//') || /[:?#\\]/u.test(path))
    throw invalid('invalid model part path');
  const parts = path.startsWith('/') ? [] : base.split('/').slice(0, -1);
  for (const segment of path.split('/')) {
    if (!segment || segment === '.') continue;
    if (segment === '..') {
      if (!parts.pop()) throw invalid('invalid model part path');
    } else {
      parts.push(segment);
    }
  }
  return parts.join('/');
};

interface Model {
  unit: number;
  root: Element;
  objects: Map<number, Element>;
  meshes: Map<number, BufferGeometry>;
}

class ThreeMFReader {
  #archive: ThreeMFArchive;
  #models = new Map<string, Model>();
  #parts: MeshPart[] = [];
  #xmlTags = 0;
  #visits = 0;
  #triangles = 0;

  constructor(bytes: Uint8Array) {
    this.#archive = new ThreeMFArchive(bytes);
  }

  async #xml(path: string): Promise<Element | undefined> {
    const bytes = await this.#archive.read(path);
    if (!bytes) return undefined;
    const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
    if (text.includes('<!DOCTYPE'))
      throw invalid('XML document type declaration');
    // Bound the number of DOM nodes before creating the document.
    for (
      let at = text.indexOf('<');
      at !== -1;
      at = text.indexOf('<', at + 1)
    ) {
      this.#xmlTags += 1;
      if (this.#xmlTags > MAX_XML_TAGS)
        throw new ModelLimitError('3MF has too many XML elements');
    }
    const doc = new DOMParser().parseFromString(text, 'application/xml');
    // Chromium and Firefox put their XML errors in different namespaces.
    if (
      doc.getElementsByTagNameNS('http://www.w3.org/1999/xhtml', 'parsererror')
        .length ||
      doc.getElementsByTagNameNS(
        'http://www.mozilla.org/newlayout/xml/parsererror.xml',
        'parsererror',
      ).length
    )
      throw invalid('malformed XML');
    return doc.documentElement;
  }

  async #rootPath(): Promise<string> {
    const rels = await this.#xml('_rels/.rels');
    if (!rels) return DEFAULT_MODEL;
    if (rels.localName !== 'Relationships' || rels.namespaceURI !== RELS)
      throw invalid('missing package relationships');
    for (const rel of children(rels, 'Relationship')) {
      if (rel.getAttribute('Type') !== MODEL_REL) continue;
      if (rel.getAttribute('TargetMode') === 'External')
        throw invalid('external model part');
      return packagePath('', rel.getAttribute('Target') ?? '');
    }
    return DEFAULT_MODEL;
  }

  async #model(path: string): Promise<Model> {
    const key = path.toLowerCase();
    const cached = this.#models.get(key);
    if (cached) return cached;
    const root = await this.#xml(path);
    if (root?.localName !== 'model' || root.namespaceURI !== CORE)
      throw invalid('missing model part');
    const unit = units.get(root.getAttribute('unit') ?? 'millimeter');
    if (unit === undefined) throw invalid('unknown unit');
    const required = root.getAttribute('requiredextensions')?.trim();
    for (const prefix of required ? required.split(/\s+/u) : []) {
      const namespace = root.lookupNamespaceURI(prefix);
      if (namespace !== PRODUCTION && namespace !== MATERIAL)
        throw invalid('unsupported required extension');
    }
    const resources = child(root, 'resources');
    if (!resources) throw invalid('missing resources');
    const objects = new Map<number, Element>();
    for (const object of children(resources, 'object')) {
      const id = idOf(object, 'id');
      if (id === 0 || objects.has(id))
        throw invalid('duplicate or zero object ID');
      objects.set(id, object);
    }
    const model = {
      unit,
      root,
      objects,
      meshes: new Map<number, BufferGeometry>(),
    };
    this.#models.set(key, model);
    return model;
  }

  #addMesh(model: Model, id: number, mesh: Element, transform: Matrix4): void {
    const saved = model.meshes.get(id);
    const triangleCount = saved
      ? (saved.index?.count ?? 0) / 3
      : child(mesh, 'triangles')?.childElementCount;
    if (!triangleCount) throw invalid('empty mesh');
    this.#triangles += triangleCount;
    if (this.#triangles > MAX_TRIANGLES)
      throw new ModelLimitError('3MF has more than 300,000 triangles');
    const geometry = saved ?? readMesh(mesh, model.unit, triangleCount);
    model.meshes.set(id, geometry);
    this.#parts.push({ geometry, transform });
  }

  async #visit(
    parentPath: string,
    parent: Model,
    ref: Element,
    parentTransform: Matrix4,
    ancestors: Set<Element>,
  ): Promise<void> {
    this.#visits += 1;
    if (this.#visits > MAX_VISITS || ancestors.size >= MAX_DEPTH)
      throw new ModelLimitError('3MF has too many component references');

    const target = ref.getAttributeNS(PRODUCTION, 'path');
    const path = target === null ? parentPath : packagePath(parentPath, target);
    const model = target === null ? parent : await this.#model(path);
    const id = idOf(ref, 'objectid');
    const object = model.objects.get(id);
    if (!object || ancestors.has(object))
      throw invalid('missing or circular object reference');
    const transform = parentTransform
      .clone()
      .multiply(transformOf(ref, parent.unit));
    const mesh = child(object, 'mesh');
    const components = child(object, 'components');
    if (mesh && !components) {
      this.#addMesh(model, id, mesh, transform);
    } else if (components && !mesh && child(components, 'component')) {
      ancestors.add(object);
      for (const component of children(components, 'component'))
        await this.#visit(path, model, component, transform, ancestors);
      ancestors.delete(object);
    } else {
      throw invalid('object must contain a mesh or components');
    }
  }

  async read(): Promise<MeshPart[]> {
    try {
      const path = await this.#rootPath();
      const model = await this.#model(path);
      const build = child(model.root, 'build');
      if (!build) throw invalid('missing build');
      for (const item of children(build, 'item')) {
        const printable = item.getAttribute('printable')?.trim();
        if (printable === '0' || printable === 'false') continue;
        await this.#visit(path, model, item, new Matrix4(), new Set());
      }
      if (!this.#parts.length) throw invalid('no printable build items');
      return this.#parts;
    } catch (error) {
      for (const model of this.#models.values())
        for (const mesh of model.meshes.values()) mesh.dispose();
      throw error;
    }
  }
}

const readMesh = (
  mesh: Element,
  unit: number,
  triangleCount: number,
): BufferGeometry => {
  const vertices = child(mesh, 'vertices');
  const triangles = child(mesh, 'triangles');
  if (!vertices || !triangles) throw invalid('missing vertices or triangles');
  const vertexCount = vertices.childElementCount;
  if (vertexCount > 3 * MAX_TRIANGLES)
    throw new ModelLimitError('3MF has too many vertices');
  const position = new Float32Array(vertexCount * 3);
  let at = 0;
  for (const vertex of children(vertices, 'vertex')) {
    for (const axis of ['x', 'y', 'z']) {
      const mm = decimal(vertex.getAttribute(axis)?.trim() ?? '') * unit;
      if (Math.abs(mm) > MAX_COORDINATE_MM)
        throw invalid('vertex outside coordinate range');
      position[at] = mm;
      at += 1;
    }
  }
  if (at !== position.length) throw invalid('invalid vertex element');

  const index = new Uint32Array(triangleCount * 3);
  at = 0;
  for (const triangle of children(triangles, 'triangle')) {
    for (const corner of ['v1', 'v2', 'v3']) {
      const vertex = idOf(triangle, corner);
      if (vertex >= vertexCount) throw invalid('missing triangle vertex');
      index[at] = vertex;
      at += 1;
    }
  }
  if (at !== index.length) throw invalid('invalid triangle element');

  return new BufferGeometry()
    .setAttribute('position', new BufferAttribute(position, 3))
    .setIndex(new BufferAttribute(index, 1));
};

export const threeMFReader: MeshReader = {
  matches: (bytes) =>
    bytes.length >= 4 &&
    new DataView(bytes.buffer, bytes.byteOffset, 4).getUint32(0, true) ===
      LOCAL_FILE,
  read: async (bytes) => ({
    parts: await new ThreeMFReader(bytes).read(),
    preservePlacement: true,
  }),
};
