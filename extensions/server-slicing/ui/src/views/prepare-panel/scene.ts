import {
  AmbientLight,
  BoxGeometry,
  BufferGeometry,
  DirectionalLight,
  Float32BufferAttribute,
  Group,
  LineBasicMaterial,
  LineSegments,
  Mesh,
  MeshPhongMaterial,
  OrthographicCamera,
  Plane,
  Raycaster,
  Scene,
  Vector2,
  Vector3,
  WebGLRenderer,
  type Matrix4,
} from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';

import type { STLBed } from '../../api';
import { bedCenter } from './model';

export interface PlateScene {
  bed(bed: STLBed): void;
  model(
    geometry: BufferGeometry | undefined,
    matrix: Matrix4 | undefined,
    inside: boolean,
    movable: boolean,
    position: [number, number],
  ): void;
  view(top?: boolean): void;
  dispose(): void;
}

const BED_THICKNESS = 3;
const GRID_SPACING = 10;
const VIEW_PADDING = 1.35;

const gridFor = (
  width: number,
  depth: number,
): LineSegments<BufferGeometry, LineBasicMaterial> => {
  const points: number[] = [];
  for (let x = 0; x <= width; x += GRID_SPACING) {
    points.push(x - width / 2, -depth / 2, 0, x - width / 2, depth / 2, 0);
  }
  for (let y = 0; y <= depth; y += GRID_SPACING) {
    points.push(-width / 2, y - depth / 2, 0, width / 2, y - depth / 2, 0);
  }
  return new LineSegments(
    new BufferGeometry().setAttribute(
      'position',
      new Float32BufferAttribute(points, 3),
    ),
    new LineBasicMaterial({ color: 0x53616e }),
  );
};

export const plateScene = (
  canvas: HTMLCanvasElement,
  onMove: (point: [number, number]) => void,
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
  let movable = false;
  let position: [number, number] = [0, 0];
  let drag: { pointer: number; x: number; y: number } | undefined;

  const ray = new Raycaster();
  const pointer = new Vector2();
  const intersection = new Vector3();
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
  const beginDrag = (event: PointerEvent): void => {
    if (!movable || !event.isPrimary || event.button !== 0) return;
    aim(event);
    modelMesh.updateWorldMatrix(true, false);
    if (ray.intersectObject(modelMesh, false).length === 0) return;
    if (!ray.ray.intersectPlane(floor, intersection)) return;
    drag = {
      pointer: event.pointerId,
      x: position[0] - intersection.x,
      y: position[1] - intersection.y,
    };
    canvas.setPointerCapture(event.pointerId);
    controls.enabled = false;
  };
  const moveDrag = (event: PointerEvent): void => {
    if (drag?.pointer !== event.pointerId || !movable) return;
    aim(event);
    if (!ray.ray.intersectPlane(floor, intersection)) return;
    onMove([
      roundMM(intersection.x + drag.x),
      roundMM(intersection.y + drag.y),
    ]);
  };
  const endDrag = (event: PointerEvent): void => {
    if (drag?.pointer !== event.pointerId) return;
    drag = undefined;
    controls.enabled = true;
  };

  const light = new DirectionalLight(0xffffff, 2.4);
  light.position.set(-120, -180, 260);
  scene.add(light, new AmbientLight(0xffffff, 1.7));
  const bedGroup = new Group();
  const modelMaterial = new MeshPhongMaterial({
    color: 0xff861f,
    shininess: 22,
  });
  const modelMesh = new Mesh(new BufferGeometry(), modelMaterial);
  modelMesh.visible = false;
  modelMesh.matrixAutoUpdate = false;
  scene.add(bedGroup, modelMesh);

  const bedMaterial = new MeshPhongMaterial({ color: 0x252c34 });
  let plate = new Mesh(new BoxGeometry(), bedMaterial);
  let grid = gridFor(0, 0);
  bedGroup.add(plate, grid);
  let span = 250;
  let center: [number, number] = [0, 0];
  let lastBed: STLBed | undefined;
  let frame: number | undefined;
  const draw = (): void => {
    if (frame !== undefined) return;
    frame = window.requestAnimationFrame(() => {
      frame = undefined;
      renderer.render(scene, camera);
    });
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

  const view = (top = false): void => {
    const [x, y] = center;
    controls.target.set(x, y, 0);
    camera.zoom = 1;
    if (top) camera.position.set(x, y - span / 10000, span * 3);
    else camera.position.set(x + span, y - span * 1.55, span * 1.55);
    controls.update();
    resize();
  };

  const observer = new ResizeObserver(resize);
  observer.observe(canvas);
  controls.addEventListener('change', draw);
  canvas.addEventListener('pointerdown', beginDrag, { capture: true });
  canvas.addEventListener('pointermove', moveDrag);
  canvas.addEventListener('lostpointercapture', endDrag);
  const empty = modelMesh.geometry;

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
      span = Math.max(width, depth, bed.height);
      center = bedCenter(bed);
      bedGroup.position.set(...center, 0);
      bedGroup.remove(plate, grid);
      plate.geometry.dispose();
      grid.geometry.dispose();
      grid.material.dispose();
      plate = new Mesh(
        new BoxGeometry(width, depth, BED_THICKNESS),
        bedMaterial,
      );
      plate.position.z = -BED_THICKNESS / 2 - 0.08;
      grid = gridFor(width, depth);
      grid.position.z = -0.04;
      bedGroup.add(plate, grid);
      view();
    },
    model(geometry, matrix, inside, enabled, at) {
      position = at;
      movable = enabled && geometry !== undefined && matrix !== undefined;
      modelMesh.visible = geometry !== undefined && matrix !== undefined;
      if (geometry && matrix) {
        modelMesh.geometry = geometry;
        modelMesh.matrix.copy(matrix);
        modelMesh.matrixWorldNeedsUpdate = true;
        modelMaterial.color.setHex(inside ? 0xff861f : 0xf05454);
      }
      draw();
    },
    view,
    dispose() {
      if (frame !== undefined) window.cancelAnimationFrame(frame);
      observer.disconnect();
      controls.dispose();
      canvas.removeEventListener('pointerdown', beginDrag, { capture: true });
      canvas.removeEventListener('pointermove', moveDrag);
      canvas.removeEventListener('lostpointercapture', endDrag);
      plate.geometry.dispose();
      bedMaterial.dispose();
      grid.geometry.dispose();
      grid.material.dispose();
      modelMaterial.dispose();
      empty.dispose();
      renderer.dispose();
    },
  };
};
