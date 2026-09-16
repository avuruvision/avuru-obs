import * as THREE from "three";
import { RoundedBoxGeometry } from "three/addons/geometries/RoundedBoxGeometry.js";
import type { Position } from "./model";

export interface Palette { primary: string; surface: string; raised: string; text: string; border: string; node: string; hardware: string; error: string; }
export function palette(element: HTMLElement): Palette {
  const cs = getComputedStyle(element);
  const read = (token: string) => cs.getPropertyValue(token).trim();
  return { primary: read("--color-primary"), surface: read("--color-base-100"), raised: read("--color-base-200"), text: read("--color-base-content"), border: read("--color-neutral"), node: read("--xray-node"), hardware: read("--xray-hardware"), error: read("--color-error") };
}

// Every scene owns its geometry/materials, and disposes them as a unit. Shared
// geometry is used within that scene only, never across React mount lifetimes.
export function primitives(colors: Palette) {
  const cube = new THREE.BoxGeometry(1, 1, 1);
  const rounded = new RoundedBoxGeometry(1, 1, 1, 2, .045);
  const edges = new THREE.EdgesGeometry(cube);
  const glass: { material: THREE.MeshPhongMaterial; base: number }[] = [];
  const materials = new Set<THREE.Material>();
  function place<T extends THREE.Object3D>(object: T, parent: THREE.Object3D, position: Position, size: Position): T {
    object.position.set(...position); object.scale.set(...size); parent.add(object); return object;
  }
  function solid(parent: THREE.Object3D, position: Position, size: Position, color: string) {
    const material = new THREE.MeshPhongMaterial({ color, specular: colors.border, shininess: 45 }); materials.add(material);
    return place(new THREE.Mesh(rounded, material), parent, position, size);
  }
  function wire(parent: THREE.Object3D, position: Position, size: Position, color: string, opacity = .6) {
    const material = new THREE.LineBasicMaterial({ color, transparent: true, opacity }); materials.add(material);
    return place(new THREE.LineSegments(edges, material), parent, position, size);
  }
  function translucent(parent: THREE.Object3D, position: Position, size: Position, color: string, base = .2) {
    const material = new THREE.MeshPhongMaterial({ color, transparent: true, opacity: base, depthWrite: false, side: THREE.DoubleSide, shininess: 85, specular: colors.text });
    glass.push({ material, base }); materials.add(material);
    return place(new THREE.Mesh(cube, material), parent, position, size);
  }
  function chassis(parent: THREE.Object3D, x: number, z: number) {
    solid(parent, [x, -.75, z], [3.7, .65, 3.9], colors.raised);
    solid(parent, [x, -.4, z], [3.5, .06, 3.7], colors.border);
    wire(parent, [x, -.75, z], [3.7, .65, 3.9], colors.hardware, .3);
    for (let i = 0; i < 12; i++) solid(parent, [x - 1.5 + i * .18, -.75, z + 1.97], [.08, .3, .025], colors.surface);
    for (let i = 0; i < 2; i++) {
      solid(parent, [x - .75 + i * 1.5, -.34, z - .5], [.72, .08, .75], colors.hardware);
      for (let j = 0; j < 5; j++) solid(parent, [x - 1 + i * 1.5 + j * .12, -.25, z - .5], [.04, .13, .65], colors.hardware);
    }
    for (let i = 0; i < 4; i++) solid(parent, [x - 1.1 + i * .7, -.33, z + .8], [.12, .1, .85], colors.node);
  }
  return { solid, wire, translucent, chassis,
    opacity(value: number) { glass.forEach(({ material, base }) => { material.opacity = base * (1 - value / 100) / .28; }); },
    dispose() { cube.dispose(); rounded.dispose(); edges.dispose(); materials.forEach(m => m.dispose()); },
  };
}
