// The 3D view of the plate: the bed, the parts, and what a pointer does to
// them. It holds no state of the plate. It draws what it is handed and reports
// presses, drags and lay-flat picks, so the plate store stays the only record.
import {
  AmbientLight,
  BoxGeometry,
  BufferGeometry,
  DirectionalLight,
  Float32BufferAttribute,
  Group,
  LineBasicMaterial,
  LineSegments,
  type Matrix4,
  Mesh,
  MeshBasicMaterial,
  MeshPhongMaterial,
  OrthographicCamera,
  Plane,
  Raycaster,
  Scene,
  Vector2,
  Vector3,
  WebGLRenderer,
} from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';

import type { STLBed } from '../api';
import { coplanarFaces } from './lay-flat';
import { bedCentre, type Rect } from './part';
import type { PlateMode } from './plate.svelte';

export interface ScenePart {
  readonly id: string;
  readonly geometry: BufferGeometry;
  readonly matrix: Matrix4;
  readonly rect: Rect;
  readonly selected: boolean;
  readonly outside: boolean;
}

export interface SceneEvents {
  /** A press on a part, or on the empty bed when `id` is undefined. */
  press(id: string | undefined, additive: boolean): void;
  dragStart(): void;
  /** The offset on the bed since the drag began, in millimetres. */
  drag(dx: number, dy: number): void;
  dragEnd(): void;
  /** A press on a part in lay-flat mode, on the triangle `face`. */
  layFlat(id: string, face: number): void;
}

export interface PlateScene {
  bed(bed: STLBed): void;
  parts(parts: readonly ScenePart[]): void;
  mode(mode: PlateMode): void;
  view(top: boolean): void;
  dispose(): void;
}

// three.js takes colours as numbers. These are the theme's orange and
// destructive red and the bed's greys, as the 3D view has drawn them since
// the first Prepare panel.
const PART = 0xff861f;
const PART_SELECTED = 0xffb066;
const PART_OUTSIDE = 0xf05454;
const PART_OUTSIDE_SELECTED = 0xff8a8a;
const FACE = 0xfff1e0;
const BED = 0x252c34;
const GRID = 0x53616e;
const OUTLINE = 0xe8edf2;

const BED_THICKNESS = 3;
const GRID_SPACING = 10;
// How much room the view leaves around the bed, as a share of its larger side.
const VIEW_PADDING = 1.05;
/** How far a pointer may move and still be a click, in CSS pixels. */
const CLICK_SLOP = 4;
/** Lifts the outlines over the bed so they never sink into it. */
const LIFT = 0.05;

const gridFor = (width: number, depth: number): BufferGeometry => {
  const points: number[] = [];
  for (let x = 0; x <= width; x += GRID_SPACING)
    points.push(x - width / 2, -depth / 2, 0, x - width / 2, depth / 2, 0);
  for (let y = 0; y <= depth; y += GRID_SPACING)
    points.push(-width / 2, y - depth / 2, 0, width / 2, y - depth / 2, 0);
  return new BufferGeometry().setAttribute(
    'position',
    new Float32BufferAttribute(points, 3),
  );
};

const outlineOf = (rects: readonly Rect[]): BufferGeometry => {
  const points: number[] = [];
  for (const { minX, minY, maxX, maxY } of rects) {
    const corners = [
      [minX, minY],
      [maxX, minY],
      [maxX, maxY],
      [minX, maxY],
    ] as const;
    corners.forEach(([x, y], i) => {
      const [nx, ny] = corners[(i + 1) % 4] ?? corners[0];
      points.push(x, y, LIFT, nx, ny, LIFT);
    });
  }
  return new BufferGeometry().setAttribute(
    'position',
    new Float32BufferAttribute(points, 3),
  );
};

/** The triangles of `faces`, copied out of a mesh, for the lay-flat highlight. */
const facesOf = (
  geometry: BufferGeometry,
  faces: readonly number[],
): BufferGeometry => {
  const position = geometry.getAttribute('position');
  const points = new Float32Array(faces.length * 9);
  const point = new Vector3();
  faces.forEach((face, i) => {
    for (let corner = 0; corner < 3; corner += 1) {
      const at = face * 3 + corner;
      point.fromBufferAttribute(position, geometry.index?.getX(at) ?? at);
      point.toArray(points, i * 9 + corner * 3);
    }
  });
  return new BufferGeometry().setAttribute(
    'position',
    new Float32BufferAttribute(points, 3),
  );
};

export const plateScene = (
  canvas: HTMLCanvasElement,
  events: SceneEvents,
): PlateScene => {
  const renderer = new WebGLRenderer({ canvas, antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  const scene = new Scene();
  const camera = new OrthographicCamera();
  camera.up.set(0, 0, 1);
  const controls = new OrbitControls(camera, canvas);
  controls.maxPolarAngle = Math.PI / 2;
  controls.minZoom = 0.25;
  controls.maxZoom = 8;

  const light = new DirectionalLight(0xffffff, 2.4);
  light.position.set(-120, -180, 260);
  scene.add(light, new AmbientLight(0xffffff, 1.7));

  const materials = {
    normal: new MeshPhongMaterial({ color: PART, shininess: 22 }),
    selected: new MeshPhongMaterial({ color: PART_SELECTED, shininess: 22 }),
    outside: new MeshPhongMaterial({ color: PART_OUTSIDE, shininess: 22 }),
    outsideSelected: new MeshPhongMaterial({
      color: PART_OUTSIDE_SELECTED,
      shininess: 22,
    }),
  };
  const bedMaterial = new MeshPhongMaterial({ color: BED });
  const gridMaterial = new LineBasicMaterial({ color: GRID });
  const outlineMaterial = new LineBasicMaterial({ color: OUTLINE });
  const faceMaterial = new MeshBasicMaterial({
    color: FACE,
    polygonOffset: true,
    polygonOffsetFactor: -2,
    polygonOffsetUnits: -2,
  });

  const materialOf = (part: ScenePart): MeshPhongMaterial => {
    if (part.outside)
      return part.selected ? materials.outsideSelected : materials.outside;
    return part.selected ? materials.selected : materials.normal;
  };

  const bedGroup = new Group();
  let plate = new Mesh(new BoxGeometry(), bedMaterial);
  let grid = new LineSegments(new BufferGeometry(), gridMaterial);
  bedGroup.add(plate, grid);
  const partGroup = new Group();
  const outline = new LineSegments(new BufferGeometry(), outlineMaterial);
  const highlight = new Mesh(new BufferGeometry(), faceMaterial);
  highlight.matrixAutoUpdate = false;
  highlight.visible = false;
  scene.add(bedGroup, partGroup, outline, highlight);

  const meshes = new Map<string, Mesh>();
  let mode: PlateMode = 'move';
  let span = 250;
  let centre: [number, number] = [0, 0];
  let lastBed: STLBed | undefined;
  let frame: number | undefined;
  let drag: { pointer: number; x: number; y: number } | undefined;
  let click: { pointer: number; x: number; y: number } | undefined;
  let hovered: { geometry: BufferGeometry; faces: Set<number> } | undefined;

  const draw = (): void => {
    if (frame !== undefined) return;
    frame = window.requestAnimationFrame(() => {
      frame = undefined;
      renderer.render(scene, camera);
    });
  };

  const ray = new Raycaster();
  const pointer = new Vector2();
  const floorPoint = new Vector3();
  const floor = new Plane(new Vector3(0, 0, 1));
  const roundMM = (value: number): number => Math.round(value * 10) / 10;
  const aim = (event: PointerEvent): void => {
    const box = canvas.getBoundingClientRect();
    pointer.set(
      ((event.clientX - box.left) / box.width) * 2 - 1,
      1 - ((event.clientY - box.top) / box.height) * 2,
    );
    ray.setFromCamera(pointer, camera);
  };
  const hit = (): { id: string; face: number } | undefined => {
    partGroup.updateMatrixWorld(true);
    const [first] = ray.intersectObjects([...meshes.values()], false);
    const id = first?.object.name;
    return first && id ? { id, face: first.faceIndex ?? 0 } : undefined;
  };

  const showFace = (target: { id: string; face: number } | undefined): void => {
    const mesh = target && meshes.get(target.id);
    if (!target || !mesh) {
      if (highlight.visible) {
        highlight.visible = false;
        hovered = undefined;
        draw();
      }
      return;
    }
    if (hovered?.geometry === mesh.geometry && hovered.faces.has(target.face)) {
      highlight.matrix.copy(mesh.matrix);
      return;
    }
    const faces = coplanarFaces(mesh.geometry, target.face);
    hovered = { geometry: mesh.geometry, faces: new Set(faces) };
    highlight.geometry.dispose();
    highlight.geometry = facesOf(mesh.geometry, faces);
    highlight.matrix.copy(mesh.matrix);
    highlight.matrixWorldNeedsUpdate = true;
    highlight.visible = true;
    draw();
  };

  const down = (event: PointerEvent): void => {
    if (!event.isPrimary || event.button !== 0) return;
    aim(event);
    const target = hit();
    if (!target) {
      click = { pointer: event.pointerId, x: event.clientX, y: event.clientY };
      return;
    }
    // The orbit controls take every press on the canvas; a press on a part
    // is the plate's, so they are switched off until it ends.
    controls.enabled = false;
    canvas.setPointerCapture(event.pointerId);
    if (mode === 'layflat') {
      events.layFlat(target.id, target.face);
      showFace(undefined);
      drag = undefined;
      return;
    }
    events.press(target.id, event.shiftKey);
    if (event.shiftKey || !ray.ray.intersectPlane(floor, floorPoint)) return;
    drag = { pointer: event.pointerId, x: floorPoint.x, y: floorPoint.y };
    events.dragStart();
  };

  const move = (event: PointerEvent): void => {
    aim(event);
    if (drag?.pointer === event.pointerId) {
      if (!ray.ray.intersectPlane(floor, floorPoint)) return;
      events.drag(
        roundMM(floorPoint.x - drag.x),
        roundMM(floorPoint.y - drag.y),
      );
      return;
    }
    if (event.buttons !== 0) return;
    const target = hit();
    canvas.dataset.hover = target ? mode : '';
    showFace(mode === 'layflat' ? target : undefined);
  };

  const up = (event: PointerEvent): void => {
    if (click?.pointer === event.pointerId) {
      const still =
        Math.hypot(event.clientX - click.x, event.clientY - click.y) <=
        CLICK_SLOP;
      click = undefined;
      if (still) events.press(undefined, event.shiftKey);
    }
  };

  const release = (event: PointerEvent): void => {
    controls.enabled = true;
    if (drag?.pointer !== event.pointerId) return;
    drag = undefined;
    events.dragEnd();
  };

  const leave = (): void => {
    canvas.dataset.hover = '';
    showFace(undefined);
  };

  const resize = (): void => {
    const { clientWidth: width, clientHeight: height } = canvas;
    if (width === 0 || height === 0) return;
    const half = (span * VIEW_PADDING) / 2;
    const aspect = width / height;
    camera.left = -half * Math.max(aspect, 1);
    camera.right = -camera.left;
    camera.top = half * Math.max(1 / aspect, 1);
    camera.bottom = -camera.top;
    camera.far = span * 16;
    camera.updateProjectionMatrix();
    renderer.setSize(width, height, false);
    draw();
  };

  const view = (top: boolean): void => {
    const [x, y] = centre;
    controls.target.set(x, y, 0);
    camera.zoom = 1;
    if (top) camera.position.set(x, y - span / 10000, span * 3);
    else camera.position.set(x + span * 0.25, y - span * 1.1, span * 0.8);
    controls.update();
    resize();
  };

  const observer = new ResizeObserver(resize);
  observer.observe(canvas);
  controls.addEventListener('change', draw);
  canvas.addEventListener('pointerdown', down, { capture: true });
  canvas.addEventListener('pointermove', move);
  canvas.addEventListener('pointerup', up);
  canvas.addEventListener('lostpointercapture', release);
  canvas.addEventListener('pointerleave', leave);

  return {
    bed(bed) {
      if (
        lastBed?.min_x === bed.min_x &&
        lastBed.min_y === bed.min_y &&
        lastBed.max_x === bed.max_x &&
        lastBed.max_y === bed.max_y &&
        lastBed.height === bed.height
      )
        return;
      lastBed = bed;
      const width = bed.max_x - bed.min_x;
      const depth = bed.max_y - bed.min_y;
      // The angled view turns the bed, so its diagonal is what has to fit.
      span = Math.hypot(width, depth);
      centre = bedCentre(bed);
      bedGroup.position.set(...centre, 0);
      bedGroup.remove(plate, grid);
      plate.geometry.dispose();
      grid.geometry.dispose();
      plate = new Mesh(
        new BoxGeometry(width, depth, BED_THICKNESS),
        bedMaterial,
      );
      plate.position.z = -BED_THICKNESS / 2 - 0.08;
      grid = new LineSegments(gridFor(width, depth), gridMaterial);
      grid.position.z = -0.04;
      bedGroup.add(plate, grid);
      view(false);
    },
    parts(parts) {
      const present = new Set(parts.map((part) => part.id));
      for (const [id, mesh] of meshes) {
        if (present.has(id)) continue;
        partGroup.remove(mesh);
        meshes.delete(id);
      }
      for (const part of parts) {
        let mesh = meshes.get(part.id);
        if (!mesh) {
          mesh = new Mesh(part.geometry, materials.normal);
          mesh.name = part.id;
          mesh.matrixAutoUpdate = false;
          meshes.set(part.id, mesh);
          partGroup.add(mesh);
        }
        mesh.geometry = part.geometry;
        mesh.matrix.copy(part.matrix);
        mesh.matrixWorldNeedsUpdate = true;
        mesh.material = materialOf(part);
      }
      outline.geometry.dispose();
      outline.geometry = outlineOf(
        parts.filter((part) => part.selected).map((part) => part.rect),
      );
      hovered = undefined;
      highlight.visible = false;
      draw();
    },
    mode(next) {
      mode = next;
      if (mode !== 'layflat') showFace(undefined);
    },
    view,
    dispose() {
      if (frame !== undefined) window.cancelAnimationFrame(frame);
      observer.disconnect();
      controls.dispose();
      canvas.removeEventListener('pointerdown', down, { capture: true });
      canvas.removeEventListener('pointermove', move);
      canvas.removeEventListener('pointerup', up);
      canvas.removeEventListener('lostpointercapture', release);
      canvas.removeEventListener('pointerleave', leave);
      plate.geometry.dispose();
      grid.geometry.dispose();
      outline.geometry.dispose();
      highlight.geometry.dispose();
      for (const material of [
        ...Object.values(materials),
        bedMaterial,
        gridMaterial,
        outlineMaterial,
        faceMaterial,
      ])
        material.dispose();
      renderer.dispose();
    },
  };
};
