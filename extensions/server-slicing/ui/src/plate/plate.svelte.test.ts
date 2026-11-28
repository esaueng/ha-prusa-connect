import {
  BoxGeometry,
  BufferGeometry,
  Float32BufferAttribute,
  Matrix4,
  Vector3,
} from 'three';
import { describe, expect, it, vi } from 'vitest';

import type { STLBed } from '../api';
import { ARRANGE_GAP_MM } from './arrange';
import { coplanarFaces, faceNormal, layFlatRotation } from './lay-flat';
import { footprintOf, type Part, worldMatrix } from './part';
import { Plate, UNDO_STEPS } from './plate.svelte';

const bed: STLBed = { min_x: 0, min_y: 0, max_x: 250, max_y: 210, height: 220 };

/** A box mesh as an STL reader answers it: triangles, no index. */
const box = (x: number, y: number, z: number): BufferGeometry =>
  new BoxGeometry(x, y, z).toNonIndexed();

const plateOf = (bedOf: () => STLBed | undefined = () => bed): Plate =>
  new Plate(bedOf);

/** Adds one box where the file put it, centred on `at`. */
const placed = (
  plate: Plate,
  at: [number, number],
  size: [number, number, number] = [10, 20, 30],
): Part => {
  const geometry = box(...size);
  geometry.translate(at[0], at[1], size[2] / 2);
  expect(
    plate.add({ parts: [{ geometry }], preservePlacement: true }, 'box.stl'),
  ).toBeUndefined();
  const part = plate.parts.at(-1);
  if (!part) throw new Error('no part was added');
  return part;
};

const corner = (part: Part): number[] => {
  const point = new Vector3();
  const corners: number[][] = [];
  const position = part.geometry.getAttribute('position');
  const matrix = worldMatrix(part);
  for (let i = 0; i < position.count; i += 1) {
    point.fromBufferAttribute(position, i).applyMatrix4(matrix);
    corners.push([point.x, point.y, point.z]);
  }
  return [
    Math.min(...corners.map((c) => c[0] ?? 0)),
    Math.min(...corners.map((c) => c[1] ?? 0)),
    Math.min(...corners.map((c) => c[2] ?? 0)),
  ].map((value) => Math.round(value * 1000) / 1000);
};

describe('the parts on the plate', () => {
  it('starts empty and adds each part of a file as a part of its own', () => {
    const plate = plateOf();
    expect(plate.parts).toEqual([]);
    expect(plate.printable).toBe(false);

    const a = box(10, 10, 10);
    const b = box(20, 20, 20);
    b.translate(40, 0, 0);
    plate.add(
      { parts: [{ geometry: a }, { geometry: b }], preservePlacement: false },
      'two.3mf',
    );

    expect(plate.parts).toHaveLength(2);
    expect([...plate.selected]).toEqual(plate.parts.map((part) => part.id));
    // Two parts with no placement go to free spots clear of each other.
    const [first, second] = plate.parts.map(footprintOf);
    if (!first || !second) throw new Error('two parts were added');
    const apart =
      first.maxX + ARRANGE_GAP_MM <= second.minX ||
      second.maxX + ARRANGE_GAP_MM <= first.minX ||
      first.maxY + ARRANGE_GAP_MM <= second.minY ||
      second.maxY + ARRANGE_GAP_MM <= first.minY;
    expect(apart).toBe(true);
    expect(plate.printable).toBe(true);
    plate.dispose();
  });

  it('keeps a part where the file put it, lifting one that is below the bed', () => {
    const plate = plateOf();
    const above = box(10, 20, 30);
    above.translate(50, 75, 40);
    const below = box(10, 20, 30);
    below.translate(150, 75, -5);
    plate.add(
      {
        parts: [{ geometry: above }, { geometry: below }],
        preservePlacement: true,
      },
      'a.3mf',
    );

    const [up, sunk] = plate.parts;
    if (!up || !sunk) throw new Error('no parts');
    expect([up.x, up.y, up.z]).toEqual([50, 75, 25]);
    expect(corner(up)).toEqual([45, 65, 25]);
    expect(corner(sunk)).toEqual([145, 65, 0]);

    // Turning a part puts it down on the bed.
    plate.press(up.id, false);
    plate.rotate();
    expect(plate.parts[0]?.z).toBe(0);
    plate.dispose();
  });

  it('applies the transform a reader gives a part', () => {
    const plate = plateOf();
    plate.add(
      {
        parts: [
          {
            geometry: box(10, 20, 30),
            transform: new Matrix4().makeTranslation(100, 100, 15),
          },
        ],
        preservePlacement: true,
      },
      'placed.3mf',
    );
    expect(plate.parts[0]?.x).toBe(100);
    expect(plate.parts[0]?.y).toBe(100);
    plate.dispose();
  });

  it('refuses a file that would take the plate over 300,000 triangles', () => {
    const plate = plateOf();
    const huge = new BufferGeometry().setAttribute(
      'position',
      new Float32BufferAttribute(new Float32Array(300_001 * 9), 3),
    );
    expect(
      plate.add({ parts: [{ geometry: huge }], preservePlacement: false }, 'x'),
    ).toBe('tooManyTriangles');
    expect(plate.parts).toEqual([]);
    plate.dispose();
  });
});

describe('selecting', () => {
  it('selects one part on a press and several with Shift', () => {
    const plate = plateOf();
    const a = placed(plate, [30, 30]);
    const b = placed(plate, [100, 30]);
    const c = placed(plate, [170, 30]);

    plate.press(a.id, false);
    expect([...plate.selected]).toEqual([a.id]);
    plate.press(b.id, true);
    plate.press(c.id, true);
    expect([...plate.selected]).toEqual([a.id, b.id, c.id]);
    // A plain press on a selected part keeps the selection, so a drag moves all.
    plate.press(b.id, false);
    expect(plate.selected.length).toBe(3);
    plate.press(b.id, true);
    expect([...plate.selected]).toEqual([a.id, c.id]);
    plate.press(undefined, true);
    expect(plate.selected.length).toBe(2);
    plate.press(undefined, false);
    expect(plate.selected.length).toBe(0);
    plate.press('not-a-part', false);
    expect(plate.selected.length).toBe(0);
    plate.dispose();
  });
});

describe('moving, turning and removing', () => {
  it('drags the whole selection and takes the drag back in one Undo', () => {
    const plate = plateOf();
    const a = placed(plate, [30, 30]);
    const b = placed(plate, [100, 30]);
    plate.press(a.id, false);
    plate.press(b.id, true);
    const steps = plate.canUndo;

    plate.beginMove();
    plate.moveBy(5, 5);
    plate.moveBy(20, 10);
    plate.endMove();
    expect(plate.parts.map((part) => [part.x, part.y])).toEqual([
      [50, 40],
      [120, 40],
    ]);
    expect(steps).toBe(true);

    plate.undo();
    expect(plate.parts.map((part) => [part.x, part.y])).toEqual([
      [30, 30],
      [100, 30],
    ]);
    plate.dispose();
  });

  it('records no step for a press that did not move', () => {
    const plate = plateOf();
    placed(plate, [30, 30]);
    plate.undo();
    expect(plate.canUndo).toBe(false);
    placed(plate, [30, 30]);
    plate.beginMove();
    plate.moveBy(0, 0);
    plate.endMove();
    plate.undo();
    expect(plate.parts).toEqual([]);
    plate.dispose();
  });

  it('turns a part 90° about Z around its own centre', () => {
    const plate = plateOf();
    const part = placed(plate, [100, 100], [10, 40, 5]);
    plate.press(part.id, false);
    plate.rotate();

    const turned = plate.parts[0];
    if (!turned) throw new Error('no part');
    expect([turned.x, turned.y]).toEqual([100, 100]);
    expect(turned.size.halfX).toBeCloseTo(20);
    expect(turned.size.halfY).toBeCloseTo(5);
    expect(corner(turned)).toEqual([80, 95, 0]);
    plate.dispose();
  });

  it('scales the selection the same on every axis, and refuses a scale out of range', () => {
    const plate = plateOf();
    const part = placed(plate, [100, 100], [10, 20, 30]);
    plate.press(part.id, false);
    expect(plate.selectionScale).toBe(100);

    plate.scale(200);
    const big = plate.parts[0];
    if (!big) throw new Error('no part');
    expect([big.x, big.y, big.z, big.scale]).toEqual([100, 100, 0, 200]);
    expect(big.size.halfX).toBeCloseTo(10);
    expect(big.size.halfY).toBeCloseTo(20);
    expect(big.size.height).toBeCloseTo(60);
    expect(corner(big)).toEqual([90, 80, 0]);

    // A scale is set against the size in the file, not against the last one.
    plate.scale(50);
    expect(plate.parts[0]?.size.height).toBeCloseTo(15);

    for (const refused of [0, 0.5, 1001, Number.NaN]) {
      const before = plate.parts;
      plate.scale(refused);
      expect(plate.parts).toBe(before);
    }

    // Too large for the volume: the part turns red and blocks the plate.
    plate.scale(1000);
    expect(plate.outside).toEqual([part.id]);
    expect(plate.printable).toBe(false);

    plate.undo();
    plate.undo();
    expect(plate.parts[0]?.scale).toBe(200);
    plate.dispose();
  });

  it('answers no shared scale for a selection of different scales', () => {
    const plate = plateOf();
    const a = placed(plate, [50, 50]);
    const b = placed(plate, [150, 50]);
    plate.press(a.id, false);
    plate.scale(150);
    plate.press(b.id, true);
    expect(plate.selectionScale).toBeUndefined();
    plate.scale(120);
    expect(plate.parts.map((part) => part.scale)).toEqual([120, 120]);
    plate.dispose();
  });

  it('removes the selection, and Undo brings it back', () => {
    const plate = plateOf();
    const a = placed(plate, [30, 30]);
    placed(plate, [100, 30]);
    plate.press(a.id, false);
    plate.remove();
    expect(plate.parts).toHaveLength(1);
    expect(plate.selected.length).toBe(0);
    plate.undo();
    expect(plate.parts.map((part) => part.id)).toContain(a.id);
    plate.dispose();
  });

  it(`keeps the last ${String(UNDO_STEPS)} steps`, () => {
    const plate = plateOf();
    const part = placed(plate, [100, 100]);
    plate.press(part.id, false);
    for (let i = 0; i < UNDO_STEPS + 10; i += 1) plate.rotate();
    let undone = 0;
    while (plate.canUndo) {
      plate.undo();
      undone += 1;
    }
    expect(undone).toBe(UNDO_STEPS);
    plate.dispose();
  });
});

describe('the print volume', () => {
  it('marks a part outside the bed and blocks the plate', () => {
    let current = $state<STLBed>(bed);
    const plate = plateOf(() => current);
    placed(plate, [100, 100]);
    expect(plate.outside.length).toBe(0);

    const edge = placed(plate, [245, 100]);
    expect(plate.outside.includes(edge.id)).toBe(false);
    expect(footprintOf(edge).maxX).toBe(250);

    const over = placed(plate, [248, 100]);
    expect([...plate.outside]).toEqual([over.id]);
    expect(plate.printable).toBe(false);

    const tall = placed(plate, [60, 60], [10, 10, 230]);
    expect(plate.outside.includes(tall.id)).toBe(true);

    // A printer with a smaller bed turns the part near the edge red too.
    current = { ...bed, max_x: 200 };
    expect(plate.outside.includes(edge.id)).toBe(true);
    plate.dispose();
  });
});

describe('arranging', () => {
  it('arranges every part clear of the others inside the bed', () => {
    const plate = plateOf();
    for (let i = 0; i < 6; i += 1) placed(plate, [10, 10], [40, 30, 10]);
    expect(plate.outside.length).toBeGreaterThan(0);

    expect(plate.arrange('all')).toBe(0);
    expect(plate.outside.length).toBe(0);
    const rects = plate.parts.map(footprintOf);
    for (const [i, a] of rects.entries()) {
      for (const b of rects.slice(i + 1)) {
        const clear =
          a.maxX + ARRANGE_GAP_MM <= b.minX + 1e-9 ||
          b.maxX + ARRANGE_GAP_MM <= a.minX + 1e-9 ||
          a.maxY + ARRANGE_GAP_MM <= b.minY + 1e-9 ||
          b.maxY + ARRANGE_GAP_MM <= a.minY + 1e-9;
        expect(clear).toBe(true);
      }
    }
    plate.dispose();
  });

  it('arranges the selection around the parts that stay', () => {
    const plate = plateOf();
    const stays = placed(plate, [125, 105], [60, 60, 10]);
    const a = placed(plate, [125, 105]);
    const b = placed(plate, [125, 105]);
    plate.press(a.id, false);
    plate.press(b.id, true);

    plate.arrange('selection');
    const kept = plate.parts.find((part) => part.id === stays.id);
    expect([kept?.x, kept?.y]).toEqual([125, 105]);
    const keptRect = footprintOf(stays);
    for (const part of plate.parts.filter((one) => one.id !== stays.id)) {
      const rect = footprintOf(part);
      const clear =
        rect.maxX + ARRANGE_GAP_MM <= keptRect.minX + 1e-9 ||
        keptRect.maxX + ARRANGE_GAP_MM <= rect.minX + 1e-9 ||
        rect.maxY + ARRANGE_GAP_MM <= keptRect.minY + 1e-9 ||
        keptRect.maxY + ARRANGE_GAP_MM <= rect.minY + 1e-9;
      expect(clear).toBe(true);
    }
    plate.dispose();
  });

  it('answers how many parts found no room, and leaves them where they were', () => {
    const plate = plateOf();
    placed(plate, [60, 105], [110, 200, 10]);
    placed(plate, [190, 105], [110, 200, 10]);
    const third = placed(plate, [125, 105], [110, 200, 10]);
    expect(plate.arrange('all')).toBe(1);
    const kept = plate.parts.find((part) => part.id === third.id);
    expect([kept?.x, kept?.y]).toEqual([125, 105]);
    plate.dispose();
  });

  it('adds a copy of the selection in free space and selects it', () => {
    const plate = plateOf();
    const part = placed(plate, [100, 100]);
    plate.press(part.id, false);

    expect(plate.copy()).toBe(1);
    expect(plate.parts).toHaveLength(2);
    const copy = plate.parts[1];
    if (!copy) throw new Error('no copy');
    expect([...plate.selected]).toEqual([copy.id]);
    expect(copy.geometry).toBe(part.geometry);
    expect(plate.outside.length).toBe(0);
    const a = footprintOf(part);
    const b = footprintOf(copy);
    expect(
      a.maxX + ARRANGE_GAP_MM <= b.minX ||
        b.maxX + ARRANGE_GAP_MM <= a.minX ||
        a.maxY + ARRANGE_GAP_MM <= b.minY ||
        b.maxY + ARRANGE_GAP_MM <= a.minY,
    ).toBe(true);
    plate.dispose();
  });
});

describe('no room', () => {
  const small: STLBed = {
    min_x: 0,
    min_y: 0,
    max_x: 100,
    max_y: 100,
    height: 100,
  };

  it('moves nothing when Arrange finds no room for every part', () => {
    const plate = new Plate(() => small);
    const a = placed(plate, [25, 50], [50, 90, 10]);
    const b = placed(plate, [75, 50], [48, 90, 10]);
    expect(plate.overlapping).toEqual([]);

    // Gaps of 6 mm leave room for one of the two, so neither moves.
    expect(plate.arrange('all')).toBe(1);
    expect(plate.parts.map((part) => [part.x, part.y])).toEqual([
      [a.x, a.y],
      [b.x, b.y],
    ]);
    expect(plate.overlapping).toEqual([]);
    expect(plate.canUndo).toBe(true);
    plate.undo();
    plate.undo();
    expect(plate.canUndo).toBe(false);
    plate.dispose();
  });

  it('adds no copy when the bed has no free space for it', () => {
    const plate = new Plate(() => small);
    const part = placed(plate, [50, 50], [70, 70, 10]);
    plate.press(part.id, false);
    const before = plate.parts;

    expect(plate.copy()).toBe('noRoom');
    expect(plate.parts).toBe(before);
    expect(plate.selected).toEqual([part.id]);
    plate.dispose();
  });

  it('flags parts whose boxes overlap at the same height', () => {
    const plate = plateOf();
    const a = placed(plate, [50, 50], [20, 20, 10]);
    const b = placed(plate, [60, 50], [20, 20, 10]);
    expect(plate.overlapping).toEqual([a.id, b.id]);
    // A part standing above another, as in a 3MF assembly, does not overlap it.
    const above = box(20, 20, 10);
    above.translate(150, 50, 15);
    const under = box(20, 20, 10);
    under.translate(150, 50, 5);
    plate.add(
      {
        parts: [{ geometry: under }, { geometry: above }],
        preservePlacement: true,
      },
      'stack.3mf',
    );
    expect(plate.overlapping).toEqual([a.id, b.id]);
    expect(plate.printable).toBe(true);
    plate.dispose();
  });
});

describe('the plate revision', () => {
  it('moves on with every change, however small, and on nothing else', () => {
    const plate = plateOf();
    const part = placed(plate, [100, 100]);
    plate.press(part.id, false);
    let seen = plate.revision;
    const moved = (): boolean => {
      const changed = plate.revision !== seen;
      seen = plate.revision;
      return changed;
    };

    // A change the old rounded matrices could not tell apart.
    plate.scale(100.004);
    expect(moved()).toBe(true);
    plate.press(part.id, false);
    plate.selectAll();
    expect(moved()).toBe(false);
    plate.beginMove();
    plate.moveBy(0.001, 0);
    expect(moved()).toBe(true);
    plate.endMove();
    plate.rotate();
    expect(moved()).toBe(true);
    plate.undo();
    expect(moved()).toBe(true);
    plate.scale(Number.NaN);
    expect(moved()).toBe(false);
    plate.dispose();
  });
});

describe('Undo during a drag', () => {
  it('ends the drag, so the part Undo removed does not come back', () => {
    const plate = plateOf();
    const part = placed(plate, [100, 100]);
    const freed = vi.fn();
    part.geometry.addEventListener('dispose', freed);
    plate.press(part.id, false);
    plate.beginMove();
    plate.moveBy(5, 0);

    plate.undo();
    expect(plate.parts).toEqual([]);
    expect(freed).toHaveBeenCalledTimes(1);
    plate.moveBy(10, 0);
    plate.endMove();
    expect(plate.parts).toEqual([]);
    plate.dispose();
  });
});

describe('the meshes the plate holds', () => {
  const watched = (plate: Plate, at: [number, number]) => {
    const part = placed(plate, at);
    const freed = vi.fn();
    part.geometry.addEventListener('dispose', freed);
    return { part, freed };
  };

  it('frees a removed part’s mesh once Undo no longer holds it', () => {
    const plate = plateOf();
    const { part, freed } = watched(plate, [50, 50]);
    const other = placed(plate, [150, 50]);
    plate.press(part.id, false);
    plate.remove();
    expect(freed).not.toHaveBeenCalled();

    plate.press(other.id, false);
    for (let i = 0; i < UNDO_STEPS; i += 1) plate.rotate();
    expect(freed).toHaveBeenCalledTimes(1);
    plate.dispose();
  });

  it('frees a part’s mesh when Undo takes back its adding', () => {
    const plate = plateOf();
    const { freed } = watched(plate, [50, 50]);
    plate.undo();
    expect(plate.parts).toEqual([]);
    expect(freed).toHaveBeenCalledTimes(1);
    plate.dispose();
  });

  it('keeps a mesh a copy still uses', () => {
    const plate = plateOf();
    const { part, freed } = watched(plate, [50, 50]);
    plate.press(part.id, false);
    plate.copy();
    plate.press(part.id, false);
    plate.remove();
    plate.selectAll();
    for (let i = 0; i < UNDO_STEPS + 1; i += 1) plate.rotate();
    expect(freed).not.toHaveBeenCalled();
    plate.dispose();
  });
});

describe('lay flat', () => {
  it('finds every triangle of the flat face under the pointer', () => {
    // A box's 12 triangles: each side is two of them.
    const geometry = box(10, 20, 30);
    for (let face = 0; face < 12; face += 1) {
      const faces = coplanarFaces(geometry, face);
      expect(faces).toHaveLength(2);
      expect(faces).toContain(face);
    }
    geometry.dispose();
  });

  it('takes two coplanar islands as one face', () => {
    const left = box(10, 10, 10);
    const right = box(10, 10, 10).translate(30, 0, 0);
    const joined = new BufferGeometry().setAttribute(
      'position',
      new Float32BufferAttribute(
        [
          ...left.getAttribute('position').array,
          ...right.getAttribute('position').array,
        ],
        3,
      ),
    );
    // A three.js box lists its sides as +X, -X, +Y, -Y, +Z, -Z, two
    // triangles each, so triangles 6 and 7 are the -Y side of the first box.
    const normal = faceNormal(joined, 6);
    expect(normal.y).toBeCloseTo(-1);
    const faces = coplanarFaces(joined, 6);
    expect(faces).toEqual([6, 7, 18, 19]);
    for (const one of [left, right, joined]) one.dispose();
  });

  it('turns the chosen face to point straight down', () => {
    for (const normal of [
      new Vector3(1, 0, 0),
      new Vector3(0, 0, 1),
      new Vector3(0, 0, -1),
      new Vector3(1, 1, 1).normalize(),
    ]) {
      const rotation = layFlatRotation(new Matrix4(), normal);
      const down = normal.clone().transformDirection(rotation);
      expect(down.x).toBeCloseTo(0);
      expect(down.y).toBeCloseTo(0);
      expect(down.z).toBeCloseTo(-1);
    }
  });

  it('lays a part on the face pressed and drops it onto the bed', () => {
    const plate = plateOf();
    const part = placed(plate, [100, 100], [10, 20, 30]);
    // Face 0 of a three.js box is on its +X side.
    const side = faceNormal(part.geometry, 0);
    expect(side.x).toBeCloseTo(1);

    plate.layFlat(part.id, 0);
    const laid = plate.parts[0];
    if (!laid) throw new Error('no part');
    expect([laid.x, laid.y]).toEqual([100, 100]);
    expect(laid.size.height).toBeCloseTo(10);
    expect(corner(laid)[2]).toBe(0);
    const down = side.transformDirection(laid.rotation);
    expect(down.z).toBeCloseTo(-1);

    plate.undo();
    expect(plate.parts[0]?.size.height).toBeCloseTo(30);
    plate.dispose();
  });
});

describe('write behaviour', () => {
  it('replaces the parts once per change and not on a press that selects nothing new', () => {
    const plate = plateOf();
    const part = placed(plate, [50, 50]);
    const before = plate.parts;
    plate.press(part.id, false);
    plate.press(part.id, false);
    expect(plate.parts).toBe(before);
    const selected = plate.selected;
    plate.press(part.id, false);
    expect(plate.selected).toBe(selected);
    plate.dispose();
  });
});
