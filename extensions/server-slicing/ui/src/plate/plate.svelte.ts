// The parts on the build plate, which of them are selected, and the steps
// Undo walks back. A class, because the workspace and each of its controls
// read one plate (svelte-5.md). Parts are replaced whole on every change and
// never edited in place, so a snapshot for Undo is the array itself.
import { Matrix4, type BufferGeometry } from 'three';

import type { STLBed } from '../api';
import { MAX_TRIANGLES } from '../views/prepare-panel/import-limits';
import {
  combineParts,
  type ImportedMesh,
} from '../views/prepare-panel/mesh-import';
import { arrange, findFree } from './arrange';
import { faceNormal, layFlatRotation } from './lay-flat';
import {
  bedCentre,
  footprintOf,
  insideBed,
  orient,
  rotated,
  triangleCount,
  type Part,
} from './part';

export type PlateMode = 'move' | 'layflat';

export const PLATE_MODES: readonly PlateMode[] = ['move', 'layflat'];

/** How many steps Undo keeps. */
export const UNDO_STEPS = 50;

/** Where a copy that finds no free spot goes, beside the part it copies. */
const COPY_OFFSET_MM = 10;

const QUARTER_TURN = new Matrix4().makeRotationZ(Math.PI / 2);

/** The scale a part may take, in percent of its size in the file. */
export const SCALE_PERCENT = { min: 1, max: 1000 } as const;

/** Why parts were not added. */
export type AddRefusal = 'tooManyTriangles';

export class Plate {
  #parts = $state.raw<readonly Part[]>([]);
  #selected = $state.raw<readonly string[]>([]);
  #history = $state.raw<readonly (readonly Part[])[]>([]);
  #mode = $state<PlateMode>('move');
  readonly #bedOf: () => STLBed | undefined;
  #outside = $derived.by(() => {
    const bed = this.#bedOf();
    return this.#parts
      .filter((part) => !bed || !insideBed(part, bed))
      .map((part) => part.id);
  });
  #gesture: { before: readonly Part[]; moved: boolean } | undefined;
  #next = 1;
  /** Every mesh the plate took, so dispose frees one that only Undo held. */
  readonly #meshes: BufferGeometry[] = [];

  /**
   * `bedOf` answers the print volume the parts must stand in. It is read
   * where it is needed, so a plate follows the printer the page has chosen.
   */
  constructor(bedOf: () => STLBed | undefined) {
    this.#bedOf = bedOf;
  }

  /** Read-only by design: every change goes through a method that records Undo. */
  get parts(): readonly Part[] {
    return this.#parts;
  }

  /** The ids of the selected parts, in the order they were selected. */
  get selected(): readonly string[] {
    return this.#selected;
  }

  get selection(): readonly Part[] {
    return this.#parts.filter((part) => this.#selected.includes(part.id));
  }

  /** The parts that stand outside the print volume, or every part without one. */
  get outside(): readonly string[] {
    return this.#outside;
  }

  get bed(): STLBed | undefined {
    return this.#bedOf();
  }

  get mode(): PlateMode {
    return this.#mode;
  }

  set mode(value: PlateMode) {
    if (PLATE_MODES.includes(value)) this.#mode = value;
  }

  get canUndo(): boolean {
    return this.#history.length > 0;
  }

  get triangles(): number {
    return this.#parts.reduce(
      (total, part) => total + triangleCount(part.geometry),
      0,
    );
  }

  /** True when there is something to print and every part fits. */
  get printable(): boolean {
    return this.#parts.length > 0 && this.#outside.length === 0;
  }

  /**
   * Puts every part of an imported file on the plate and selects them. A part
   * that keeps its placement stands where the file put it; any other goes to
   * the free spot nearest the centre. The plate takes the meshes.
   */
  add(imported: ImportedMesh, name: string): AddRefusal | undefined {
    const incoming = imported.parts.reduce(
      (total, part) => total + triangleCount(part.geometry),
      0,
    );
    if (this.triangles + incoming > MAX_TRIANGLES) {
      for (const part of imported.parts) part.geometry.dispose();
      return 'tooManyTriangles';
    }

    // Each part becomes a mesh of its own, its transform applied. Two
    // instances of one object share a mesh in the file, so the file's meshes
    // are freed once every part has been read from them.
    const meshes: BufferGeometry[] = [];
    const read = imported.parts
      .map((part) => part.geometry)
      .filter((mesh, at, all) => all.indexOf(mesh) === at);
    try {
      for (const part of imported.parts) meshes.push(combineParts([part]));
    } catch (error) {
      for (const mesh of read) mesh.dispose();
      for (const mesh of meshes) if (!read.includes(mesh)) mesh.dispose();
      throw error;
    }
    for (const mesh of read) if (!meshes.includes(mesh)) mesh.dispose();

    const bed = this.#bedOf();
    const placed: Part[] = [];
    const taken = this.#parts.map(footprintOf);
    for (const geometry of meshes) {
      if (!this.#meshes.includes(geometry)) this.#meshes.push(geometry);
      const rotation = new Matrix4();
      const { local, size, centre, floor } = orient(geometry, rotation);
      let at: [number, number] = centre;
      if (!imported.preservePlacement && bed)
        at = findFree(size, taken, bed) ?? bedCentre(bed);
      const part: Part = {
        id: `part-${String(this.#next++)}`,
        name,
        geometry,
        rotation,
        local,
        size,
        x: at[0],
        y: at[1],
        // A part kept where the file put it keeps its height; one below the
        // bed is lifted onto it.
        z: imported.preservePlacement ? Math.max(0, floor) : 0,
        scale: 100,
      };
      taken.push(footprintOf(part));
      placed.push(part);
    }

    this.#record();
    this.#parts = [...this.#parts, ...placed];
    this.#selected = placed.map((part) => part.id);
    return undefined;
  }

  /**
   * A press on a part. A plain press selects that part alone, unless it is
   * selected already, so a drag moves the whole selection. With Shift it is
   * added to the selection or taken out of it. A press on the empty bed
   * clears the selection, and with Shift keeps it.
   */
  press(id: string | undefined, additive: boolean): void {
    if (id === undefined) {
      if (!additive && this.#selected.length > 0) this.#selected = [];
      return;
    }
    if (!this.#parts.some((part) => part.id === id)) return;
    if (additive) {
      this.#selected = this.#selected.includes(id)
        ? this.#selected.filter((one) => one !== id)
        : [...this.#selected, id];
      return;
    }
    if (!this.#selected.includes(id)) this.#selected = [id];
  }

  selectAll(): void {
    this.#selected = this.#parts.map((part) => part.id);
  }

  clearSelection(): void {
    if (this.#selected.length > 0) this.#selected = [];
  }

  /** Starts a drag of the selection. Undo takes the whole drag back in one step. */
  beginMove(): void {
    this.#gesture = { before: this.#parts, moved: false };
  }

  /** Moves the selection by an offset from where the drag began. */
  moveBy(dx: number, dy: number): void {
    const gesture = this.#gesture;
    if (!gesture || !Number.isFinite(dx) || !Number.isFinite(dy)) return;
    gesture.moved = gesture.moved || dx !== 0 || dy !== 0;
    this.#parts = gesture.before.map((part) =>
      this.#selected.includes(part.id)
        ? { ...part, x: part.x + dx, y: part.y + dy }
        : part,
    );
  }

  endMove(): void {
    const gesture = this.#gesture;
    this.#gesture = undefined;
    if (gesture?.moved) this.#push(gesture.before);
  }

  /** The scale every selected part shares, or undefined when they differ. */
  get selectionScale(): number | undefined {
    const scales = this.selection.map((part) => part.scale);
    const [first] = scales;
    return scales.every((one) => one === first) ? first : undefined;
  }

  /**
   * Scales each selected part to `percent` of its size in the file, the same
   * on every axis, around the centre of its footprint, and puts it on the bed.
   * A value outside SCALE_PERCENT is refused and nothing changes.
   */
  scale(percent: number): void {
    if (
      !Number.isFinite(percent) ||
      percent < SCALE_PERCENT.min ||
      percent > SCALE_PERCENT.max ||
      this.#selected.length === 0 ||
      this.selection.every((part) => part.scale === percent)
    )
      return;
    this.#change((part) => {
      const factor = percent / part.scale;
      const turned = rotated(
        part,
        new Matrix4().makeScale(factor, factor, factor).multiply(part.rotation),
      );
      return { ...turned, scale: percent };
    });
  }

  /** Turns each selected part 90° about Z, around the centre of its footprint. */
  rotate(): void {
    if (this.#selected.length === 0) return;
    this.#change((part) =>
      rotated(part, QUARTER_TURN.clone().multiply(part.rotation)),
    );
  }

  remove(): void {
    if (this.#selected.length === 0) return;
    this.#record();
    this.#parts = this.#parts.filter(
      (part) => !this.#selected.includes(part.id),
    );
    this.#selected = [];
  }

  /** Turns a part so the face of the triangle `face` lies on the bed. */
  layFlat(id: string, face: number): void {
    const part = this.#parts.find((one) => one.id === id);
    if (!part) return;
    const rotation = layFlatRotation(
      part.rotation,
      faceNormal(part.geometry, face),
    );
    this.#record();
    this.#parts = this.#parts.map((one) =>
      one.id === id ? rotated(one, rotation) : one,
    );
  }

  /**
   * Arranges every part, or only the selected ones around the others. It
   * answers how many parts found no free spot; those keep where they were.
   */
  arrange(scope: 'all' | 'selection'): number {
    const bed = this.#bedOf();
    if (!bed || this.#parts.length === 0) return 0;
    const moving =
      scope === 'all'
        ? this.#parts
        : this.#parts.filter((part) => this.#selected.includes(part.id));
    if (moving.length === 0) return 0;
    const fixed = this.#parts
      .filter((part) => !moving.includes(part))
      .map(footprintOf);
    const { placed, unplaced } = arrange(moving, fixed, bed);
    this.#record();
    this.#parts = this.#parts.map((part) => {
      const at = placed.get(part.id);
      return at ? { ...part, x: at[0], y: at[1], z: 0 } : part;
    });
    return unplaced.length;
  }

  /**
   * Adds a copy of each selected part in free space and selects the copies.
   * A copy with no free spot stands beside its part, outside the volume or on
   * another part, so it shows in red. It answers how many copies were added.
   */
  copy(): number {
    const sources = this.selection;
    if (sources.length === 0) return 0;
    const incoming = sources.reduce(
      (total, part) => total + triangleCount(part.geometry),
      0,
    );
    if (this.triangles + incoming > MAX_TRIANGLES) return 0;

    const bed = this.#bedOf();
    const taken = this.#parts.map(footprintOf);
    const copies = sources.map((part) => {
      const at = (bed && findFree(part.size, taken, bed, [part.x, part.y])) ?? [
        part.x + COPY_OFFSET_MM,
        part.y + COPY_OFFSET_MM,
      ];
      const copy: Part = {
        ...part,
        id: `part-${String(this.#next++)}`,
        x: at[0],
        y: at[1],
        z: 0,
      };
      taken.push(footprintOf(copy));
      return copy;
    });
    this.#record();
    this.#parts = [...this.#parts, ...copies];
    this.#selected = copies.map((part) => part.id);
    return copies.length;
  }

  /** Takes the last change back. The selection keeps the parts that are still there. */
  undo(): void {
    const previous = this.#history.at(-1);
    if (!previous) return;
    this.#history = this.#history.slice(0, -1);
    this.#parts = previous;
    const present = previous.map((part) => part.id);
    const kept = this.#selected.filter((id) => present.includes(id));
    if (kept.length !== this.#selected.length) this.#selected = kept;
  }

  /** Frees every mesh the plate holds, for a workspace that closes. */
  dispose(): void {
    for (const mesh of this.#meshes) mesh.dispose();
    this.#meshes.length = 0;
    this.#parts = [];
    this.#history = [];
    this.#selected = [];
  }

  #change(turn: (part: Part) => Part): void {
    this.#record();
    this.#parts = this.#parts.map((part) =>
      this.#selected.includes(part.id) ? turn(part) : part,
    );
  }

  #record(): void {
    this.#push(this.#parts);
  }

  #push(parts: readonly Part[]): void {
    this.#history = [...this.#history, parts].slice(-UNDO_STEPS);
  }
}
